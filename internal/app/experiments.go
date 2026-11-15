package app

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
)

const (
	experimentVersion   = 1
	experimentBodyLimit = 256 << 10
	// defaultExperimentRecords and defaultExperimentSizeMB are the retained
	// send history's defaults; Settings can raise or lower both.
	defaultExperimentRecords = 100
	defaultExperimentSizeMB  = 32
)

// experimentLimits bound the retained history. SpillDir holds bodies evicted
// from the log file when it would exceed Bytes; SpillBytes bounds those files
// together, so disk use stays inside the configured history size. A zero
// SpillDir keeps everything in the log and drops old records instead.
type experimentLimits struct {
	Records    int
	Bytes      int64
	SpillDir   string
	SpillBytes int64
}

func (l experimentLimits) normalized() experimentLimits {
	if l.Records <= 0 {
		l.Records = defaultExperimentRecords
	}
	if l.Bytes <= 0 {
		l.Bytes = int64(defaultExperimentSizeMB) << 20
	}
	if l.SpillBytes <= 0 {
		l.SpillBytes = l.Bytes
	}
	return l
}

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
	Tests        []request.TestResult
	Replies      []string
	Exchanges    []request.WebSocketExchange `json:",omitempty"`
	Console      []request.ConsoleMessage    `json:",omitempty"`
	Variables    []request.ResolvedVariable  `json:",omitempty"`
	Exchange     *capture.Flow
	RequestBody  []byte
	ResponseBody []byte
	// RequestBodyFile and ResponseBodyFile hold a body spilled out of the log
	// when the history size cap was reached. The inline bytes are empty then.
	RequestBodyFile  string `json:",omitempty"`
	ResponseBodyFile string `json:",omitempty"`
	Note             string
}

type experimentDraft struct {
	Key      string       `json:",omitempty"`
	Source   *debugSource `json:",omitempty"`
	Value    savedRequest
	Baseline savedRequest
	SavedKey string
	// SchemaShared says Baseline.GraphQL.Schema was omitted because it is
	// identical to Value's; loading restores it from Value.
	SchemaShared bool `json:",omitempty"`
}

type experimentLog struct {
	Version int `json:"version"`
	// NextID is the highest assigned ID; zero starts a new log.
	NextID int
	// Baseline is a record ID, not an index in Records (newest first).
	Baseline int
	Records  []experimentRecord
	Drafts   []experimentDraft
	Active   int
}

func snapshotExperiment(id int, draft savedRequest, environment string, got request.Execution) experimentRecord {
	draft.Form = slices.Clone(draft.Form)
	draft.HeaderList = slices.Clone(draft.HeaderList)
	draft.Examples = nil // kept with the request, not with every attempt
	// The schema is part of the request, not of each attempt. Keeping it in
	// every record made a 900 KiB schema exhaust the log after 36 sends.
	draft.GraphQL.Schema, draft.GraphQL.SchemaSource = "", ""
	r := experimentRecord{
		ID: id, At: time.Now(), Draft: draft, Source: draft.Source, Environment: environment,
		Summary: runSummary(got), Outcome: executionOutcome(got), Tests: slices.Clone(got.Tests), Replies: slices.Clone(got.Replies), Exchanges: slices.Clone(got.WebSocket),
		Console:   slices.Clone(got.Console),
		Variables: slices.Clone(got.Variables),
	}
	f := got.Response
	if f == nil && got.Request != nil {
		// A transport or script failure can still have a prepared request.
		f = &capture.Flow{Method: got.Request.Method, URL: got.Request.URL,
			RequestHeader: got.Request.Header, RequestBody: got.Request.Body,
			RequestBodySize: len(got.Request.Body)}
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
	f.RequestBody, f.ResponseBody = string(r.requestBody()), string(r.responseBody())
	return f
}

// requestBody and responseBody return a record's body, reading a spilled one
// back from disk. A missing spill file reads as empty rather than failing the
// whole History view.
func (r experimentRecord) requestBody() []byte {
	if len(r.RequestBody) > 0 || r.RequestBodyFile == "" {
		return r.RequestBody
	}
	return readSpill(r.RequestBodyFile)
}

func (r experimentRecord) responseBody() []byte {
	if len(r.ResponseBody) > 0 || r.ResponseBodyFile == "" {
		return r.ResponseBody
	}
	return readSpill(r.ResponseBodyFile)
}

func readSpill(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, experimentBodyLimit))
	if err != nil {
		return nil
	}
	return data
}

// spillBodies writes this record's inline bodies under dir and replaces them
// with file references. It is only called for a record already committed to
// the candidate log, so a write failure leaves the inline copy in place.
func (r *experimentRecord) spillBodies(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	write := func(name string, body []byte) (string, error) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return "", err
		}
		return path, nil
	}
	if len(r.RequestBody) > 0 {
		path, err := write(strconv.Itoa(r.ID)+"-request.bin", r.RequestBody)
		if err != nil {
			return err
		}
		r.RequestBodyFile, r.RequestBody = path, nil
	}
	if len(r.ResponseBody) > 0 {
		path, err := write(strconv.Itoa(r.ID)+"-response.bin", r.ResponseBody)
		if err != nil {
			return err
		}
		r.ResponseBodyFile, r.ResponseBody = path, nil
	}
	return nil
}

// dropSpill removes a record's spilled bodies. It is safe to call for a record
// with none.
func (r *experimentRecord) dropSpill() {
	for _, path := range []string{r.RequestBodyFile, r.ResponseBodyFile} {
		if path != "" {
			_ = os.Remove(path)
		}
	}
	r.RequestBodyFile, r.ResponseBodyFile = "", ""
}

// spillUsage is the bytes the candidate log's spill files occupy.
func (log *experimentLog) spillUsage() int64 {
	var used int64
	for _, record := range log.Records {
		for _, path := range []string{record.RequestBodyFile, record.ResponseBodyFile} {
			if info, err := os.Stat(path); err == nil {
				used += info.Size()
			}
		}
	}
	return used
}

// spillOldest moves the oldest inline bodies to disk, leaving the records in
// place. It reports whether anything changed.
func (log *experimentLog) spillOldest(limits experimentLimits) bool {
	if limits.SpillDir == "" {
		return false
	}
	used := log.spillUsage()
	for i := len(log.Records) - 1; i >= 0; i-- {
		record := &log.Records[i]
		if record.ID == log.Baseline {
			continue
		}
		size := int64(len(record.RequestBody) + len(record.ResponseBody))
		if size == 0 || used+size > limits.SpillBytes {
			continue
		}
		if err := record.spillBodies(limits.SpillDir); err != nil {
			continue
		}
		return true
	}
	return false
}

// clearSpill removes every spilled body, for a History clear.
func (log *experimentLog) clearSpill() {
	for i := range log.Records {
		log.Records[i].dropSpill()
	}
}

func loadExperiments(path string, limits experimentLimits) (experimentLog, error) {
	limits = limits.normalized()
	empty := experimentLog{Version: experimentVersion}
	if path == "" {
		return empty, nil
	}
	if info, err := os.Stat(path); err == nil && info.Size() > limits.Bytes {
		return empty, fmt.Errorf("experiment log exceeds %d bytes", limits.Bytes)
	}
	f := &store.File{Path: path, Format: store.Experiments, BackupDir: store.BackupDirFor(path)}
	data, err := f.Read()
	if err != nil {
		return empty, err
	}
	if data == nil {
		return empty, nil
	}
	if int64(len(data)) > limits.Bytes {
		return empty, fmt.Errorf("experiment log exceeds %d bytes", limits.Bytes)
	}
	var log experimentLog
	if err := json.Unmarshal(data, &log); err != nil {
		return empty, err
	}
	if _, err := encodeExperiments(log, limits); err != nil {
		return empty, err
	}
	return log, nil
}

func saveExperiments(path string, log experimentLog, limits experimentLimits) error {
	data, err := encodeExperiments(log, limits)
	if err != nil {
		return err
	}
	f := &store.File{Path: path, Format: store.Experiments, BackupDir: store.BackupDirFor(path)}
	if _, err := f.Read(); err != nil {
		return err
	}
	return f.Write(data)
}

func encodeExperiments(log experimentLog, limits experimentLimits) ([]byte, error) {
	limits = limits.normalized()
	if log.Version != experimentVersion {
		return nil, fmt.Errorf("unsupported experiment version %d", log.Version)
	}
	if len(log.Records) > limits.Records {
		return nil, fmt.Errorf("experiment log exceeds %d records", limits.Records)
	}
	data, err := json.Marshal(log, json.FormatNilSliceAsNull(true), json.FormatNilMapAsNull(true))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limits.Bytes {
		return nil, fmt.Errorf("experiment log exceeds %d bytes", limits.Bytes)
	}
	return data, nil
}

// retain commits only a complete, encodable candidate. Neither failure nor
// eviction mutates the old backing array, baseline, or editable drafts.
func (log *experimentLog) retain(record experimentRecord, limits experimentLimits) error {
	limits = limits.normalized()
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
		if len(candidate.Records) <= limits.Records && int64(len(data)) <= limits.Bytes {
			*log = candidate
			return nil
		}
		// Bodies spill to disk before a record is discarded, so a long
		// session stays in History within the configured disk budget.
		if candidate.spillOldest(limits) {
			continue
		}
		oldest := len(candidate.Records) - 1
		for oldest > 0 && candidate.Records[oldest].ID == candidate.Baseline {
			oldest--
		}
		if oldest == 0 {
			return fmt.Errorf("new experiment, baseline and drafts exceed experiment log limits")
		}
		candidate.Records[oldest].dropSpill()
		candidate.Records = slices.Delete(candidate.Records, oldest, oldest+1)
	}
}

// experimentLimits are the configured history caps. Spilled bodies live beside
// the log, and their total stays within the same size setting.
func (a *app) experimentLimits() experimentLimits {
	size := int64(cmp.Or(a.historySizeMB, defaultExperimentSizeMB)) << 20
	limits := experimentLimits{Records: cmp.Or(a.historyLimit, defaultExperimentRecords), Bytes: size, SpillBytes: size}
	if a.experiments.path != "" {
		// The directory name carries the history format version, so a newer
		// app never reads bodies written for another format.
		limits.SpillDir = filepath.Join(filepath.Dir(a.experiments.path), fmt.Sprintf("experiments-spill-v%d", experimentVersion))
	}
	return limits.normalized()
}

// applyHistoryLimits gives the writer the new caps after a Settings change.
func (a *app) applyHistoryLimits() {
	a.experiments.limits = a.experimentLimits()
}
