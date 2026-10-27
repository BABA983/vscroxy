package app

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

const (
	experimentVersion     = 1
	experimentBodyLimit   = 256 << 10
	experimentRecordLimit = 100
	experimentSizeLimit   = 32 << 20
)

type experimentRecord struct {
	ID           int
	RunKey       string        `json:",omitempty"`
	DraftKey     string        `json:",omitempty"`
	Source       *debugSource  `json:",omitempty"`
	Wire         []sessionFlow `json:",omitempty"`
	At           time.Time
	Draft        savedRequest
	Environment  string
	Summary      string
	Outcome      string `json:",omitempty"`
	Tests        []testResult
	Replies      []string
	Console      []runMessage       `json:",omitempty"`
	Variables    []resolvedVariable `json:",omitempty"`
	Exchange     *capture.Flow
	RequestBody  []byte
	ResponseBody []byte
	Note         string
}

type experimentDraft struct {
	Key      string       `json:",omitempty"`
	Source   *debugSource `json:",omitempty"`
	Value    savedRequest
	Baseline savedRequest
	SavedKey string
}

type experimentLog struct {
	Version int
	// NextID is the highest assigned ID; zero starts a new log.
	NextID int
	// Baseline is a record ID, not an index in Records (newest first).
	Baseline int
	Records  []experimentRecord
	Drafts   []experimentDraft
	Active   int
}

func snapshotExperiment(id int, draft savedRequest, environment string, got requestExecution) experimentRecord {
	draft.Form = slices.Clone(draft.Form)
	draft.HeaderList = slices.Clone(draft.HeaderList)
	r := experimentRecord{
		ID: id, At: time.Now(), Draft: draft, Source: draft.Source, Environment: environment,
		Summary: got.summary(), Outcome: got.outcome(), Tests: slices.Clone(got.tests), Replies: slices.Clone(got.replies),
		Console:   slices.Clone(got.console),
		Variables: slices.Clone(got.variables),
	}
	f := got.response
	if f == nil && got.request != nil {
		// A transport or script failure can still have a prepared request.
		f = &capture.Flow{Method: got.request.Method, URL: got.request.URL,
			RequestHeader: got.request.header, RequestBody: got.request.Body,
			RequestBodySize: len(got.request.Body)}
	}
	if f != nil {
		r.Exchange = experimentMetadata(f)
		r.RequestBody, r.Exchange.RequestBodySize, r.Exchange.RequestBodyError = experimentPayload(f.RequestBody, f.RequestBodyFile, f.RequestBodySize, f.RequestBodyError)
		r.ResponseBody, r.Exchange.ResponseBodySize, r.Exchange.ResponseBodyError = experimentPayload(f.ResponseBody, f.ResponseBodyFile, f.ResponseBodySize, f.ResponseBodyError)
	}
	return r
}

// experimentMetadata deliberately has no dependency on capture's spill files.
func experimentMetadata(f *capture.Flow) *capture.Flow {
	c := *f
	c.Actions = slices.Clone(f.Actions)
	c.RequestHeader = f.RequestHeader.Clone()
	c.ResponseHeader = f.ResponseHeader.Clone()
	c.RequestTrailer = f.RequestTrailer.Clone()
	c.ResponseTrailer = f.ResponseTrailer.Clone()
	c.RequestBody, c.ResponseBody = "", ""
	c.RequestBodyFile, c.ResponseBodyFile = "", ""
	c.Frames = nil
	return &c
}

// Bodies are bytes (JSON base64), never UTF-8 strings on disk. Diagnostics
// describe missing tails without inserting text into the retained payload.
func experimentPayload(inline, path string, size int, problem string) ([]byte, int, string) {
	size = max(size, len(inline))
	body := []byte(inline[:min(len(inline), experimentBodyLimit)])
	if path != "" {
		f, err := os.Open(path)
		if err == nil {
			if info, statErr := f.Stat(); statErr == nil {
				size = max(size, int(info.Size()))
			}
			var data []byte
			data, err = io.ReadAll(io.LimitReader(f, experimentBodyLimit+1))
			closed := f.Close()
			if err == nil {
				body = data[:min(len(data), experimentBodyLimit)]
				size = max(size, len(data))
				err = closed
			}
		}
		if err != nil {
			problem = experimentProblem(problem, "Experiment body read: "+err.Error())
		}
	}
	if size > len(body) {
		problem = experimentProblem(problem, fmt.Sprintf("Experiment body truncated: retained %d of %d bytes", len(body), size))
	}
	return body, size, problem
}

func experimentProblem(existing, added string) string {
	if existing == "" {
		return added
	}
	return existing + "; " + added
}

func (r experimentRecord) response() *capture.Flow {
	if r.Exchange == nil || r.Exchange.Status == 0 {
		return nil
	}
	f := experimentMetadata(r.Exchange)
	f.RequestBody, f.ResponseBody = string(r.RequestBody), string(r.ResponseBody)
	return f
}

func loadExperiments(path string) (experimentLog, error) {
	empty := experimentLog{Version: experimentVersion}
	if path == "" {
		return empty, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, experimentSizeLimit+1))
	if err != nil {
		return empty, err
	}
	if len(data) > experimentSizeLimit {
		return empty, fmt.Errorf("experiment log exceeds %d bytes", experimentSizeLimit)
	}
	var log experimentLog
	if err := json.Unmarshal(data, &log); err != nil {
		return empty, err
	}
	if _, err := encodeExperiments(log); err != nil {
		return empty, err
	}
	return log, nil
}

func saveExperiments(path string, log experimentLog) error {
	data, err := encodeExperiments(log)
	if err != nil {
		return err
	}
	return writePrivateFile(path, data)
}

func encodeExperiments(log experimentLog) ([]byte, error) {
	if log.Version != experimentVersion {
		return nil, fmt.Errorf("unsupported experiment version %d", log.Version)
	}
	if len(log.Records) > experimentRecordLimit {
		return nil, fmt.Errorf("experiment log exceeds %d records", experimentRecordLimit)
	}
	data, err := json.Marshal(log, json.FormatNilSliceAsNull(true), json.FormatNilMapAsNull(true))
	if err != nil {
		return nil, err
	}
	if len(data) > experimentSizeLimit {
		return nil, fmt.Errorf("experiment log exceeds %d bytes", experimentSizeLimit)
	}
	return data, nil
}

// retain commits only a complete, encodable candidate. Neither failure nor
// eviction mutates the old backing array, baseline, or editable drafts.
func (log *experimentLog) retain(record experimentRecord) error {
	candidate := *log
	if candidate.Version == 0 {
		candidate.Version = experimentVersion
	}
	if candidate.Version != experimentVersion {
		return fmt.Errorf("unsupported experiment version %d", candidate.Version)
	}
	candidate.NextID = max(0, candidate.NextID)
	for _, old := range log.Records {
		candidate.NextID = max(candidate.NextID, old.ID)
	}
	if record.ID == 0 {
		record.ID = candidate.NextID + 1
	}
	if slices.ContainsFunc(log.Records, func(old experimentRecord) bool { return old.ID == record.ID }) {
		return fmt.Errorf("experiment ID %d already exists", record.ID)
	}
	candidate.NextID = max(candidate.NextID, record.ID)
	candidate.Records = append([]experimentRecord{record}, log.Records...)
	for {
		// Marshal separately so malformed values are not mistaken for a size
		// problem and do not cause unrelated records to be discarded.
		data, err := json.Marshal(candidate, json.FormatNilSliceAsNull(true), json.FormatNilMapAsNull(true))
		if err != nil {
			return err
		}
		if len(candidate.Records) <= experimentRecordLimit && len(data) <= experimentSizeLimit {
			*log = candidate
			return nil
		}
		oldest := len(candidate.Records) - 1
		for oldest > 0 && candidate.Records[oldest].ID == candidate.Baseline {
			oldest--
		}
		if oldest == 0 {
			return fmt.Errorf("new experiment, baseline and drafts exceed experiment log limits")
		}
		candidate.Records = slices.Delete(candidate.Records, oldest, oldest+1)
	}
}
