package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/egoist/mygo/ui"
)

type columnSource struct {
	body                     inspectionBody
	method, url, contentType string
}

type columnCell struct {
	source         columnSource
	value, problem string
	pending        bool
	operations     []graphql.Operation
}

type columnJob struct {
	cell   *columnCell
	source columnSource
	column customColumn
}

type columnAnswer struct {
	cell           *columnCell
	value, problem string
	operations     []graphql.Operation
}

// A single bounded reader publishes small batches. Rendering and sorting only
// read its UI-owned cache; workers only see immutable source snapshots.
type columnReader struct {
	dirty   bool
	queue   []columnJob
	cancel  context.CancelFunc
	answers chan []columnAnswer
}

func (r *columnReader) stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.cancel, r.answers, r.queue = nil, nil, nil
}

func (a *app) syncColumnValues(c *ui.Context) {
	r := &a.columnRead
	changed := false
	if r.answers != nil {
		select {
		case answers := <-r.answers:
			r.cancel()
			r.cancel, r.answers = nil, nil
			for _, answer := range answers {
				answer.cell.value, answer.cell.problem, answer.cell.pending = answer.value, answer.problem, false
				answer.cell.operations = answer.operations
			}
			changed = true
		default:
		}
	}
	if r.dirty {
		r.dirty = false
		columns := slices.DeleteFunc(slices.Clone(a.customColumns), func(cc customColumn) bool {
			return slices.Contains(a.hiddenColumns, cc.id())
		})
		if a.filterTriage.Operation != "" && !slices.ContainsFunc(columns, func(cc customColumn) bool { return cc.Source == sourceGraphQL }) {
			columns = append(slices.Clone(columns), graphqlColumn)
		}
		for _, cc := range columns {
			if !cc.derived() {
				continue
			}
			for i := range a.flows {
				f := &a.flows[i]
				source := cc.inspectionSource(*f)
				if f.columnData == nil {
					f.columnData = make(map[string]*columnCell)
				}
				cell := f.columnData[cc.id()]
				if cell != nil && cell.source == source {
					continue
				}
				cell = &columnCell{source: source, pending: true}
				f.columnData[cc.id()] = cell
				if cc.body() && source.body == (inspectionBody{}) {
					cell.pending = false // an absent HTTP body is not malformed JSON
					continue
				}
				r.queue = append(r.queue, columnJob{cell, source, cc})
			}
		}
	}
	if r.answers == nil && len(r.queue) > 0 {
		n := min(32, len(r.queue))
		jobs := append([]columnJob(nil), r.queue[:n]...)
		clear(r.queue[:n])
		r.queue = r.queue[n:]
		ctx, cancel := context.WithCancel(context.Background())
		answers := make(chan []columnAnswer, 1)
		r.cancel, r.answers = cancel, answers
		services := c.Services()
		go func() {
			results := make([]columnAnswer, 0, len(jobs))
			for _, job := range jobs {
				if ctx.Err() != nil {
					return
				}
				text, err := job.source.body.read(ctx)
				answer := columnAnswer{cell: job.cell}
				if err == nil {
					if job.column.Source == sourceGraphQL {
						answer.operations, err = graphql.RequestOperations(job.source.method, job.source.url, job.source.contentType, text)
						var labels []string
						for _, op := range answer.operations {
							name := op.Name
							if name == "" {
								name = "(anonymous)"
							}
							labels = append(labels, op.Type+" "+name)
						}
						answer.value = strings.Join(labels, ", ")
						if len(answer.value) > 4096 {
							answer.value = answer.value[:4093] + "..." // operation names are ASCII; matching keeps the complete names
						}
					} else {
						answer.value, err = inspect.JSONField(ctx, text, job.column.Name)
					}
				}
				if err != nil {
					answer.problem = err.Error()
				}
				results = append(results, answer)
			}
			select {
			case answers <- results:
				services.Invalidate()
			case <-ctx.Done():
			}
		}()
	}
	if r.answers != nil {
		c.After(50 * time.Millisecond)
	}
	if changed {
		a.applyFilter() // sorting follows the values, not worker completion order
	}
}

func (cc customColumn) body() bool {
	return cc.Source == sourceRequestJSON || cc.Source == sourceResponseJSON
}

func (cc customColumn) derived() bool {
	return cc.body() || cc.Source == sourceGraphQL
}

func (cc customColumn) inspectionSource(f flow) columnSource {
	side := 0
	if cc.Source == sourceRequestJSON || cc.Source == sourceGraphQL {
		side = 1
	}
	source := columnSource{body: capturedInspectionBody(f, side)}
	if cc.Source == sourceGraphQL {
		source.method, source.url, source.contentType = f.method, f.url, f.reqHeader.Get("Content-Type")
		if f.method != "POST" {
			source.body = inspectionBody{} // GET selection comes only from the URL
		}
	}
	return source
}

func (a *app) columnValue(cc customColumn, f flow) string {
	if !cc.derived() {
		return cc.value(f)
	}
	if cell := f.columnData[cc.id()]; cell != nil {
		return cell.value
	}
	return ""
}

func (f flow) hasOperation(name string) bool {
	cell := f.columnData[graphqlColumn.id()]
	return cell != nil && cell.source == graphqlColumn.inspectionSource(f) && !cell.pending && cell.problem == "" && slices.ContainsFunc(cell.operations, func(op graphql.Operation) bool {
		return op.Name == name
	})
}
