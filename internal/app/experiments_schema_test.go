package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func TestExperimentSnapshotDropsSchema(t *testing.T) {
	draft := savedRequest{Request: request.Request{
		GraphQL: request.GraphQLBody{Query: "query Q { viewer { id } }", OperationName: "Q", Schema: "type Query { viewer: User }", SchemaSource: "https://api.example.com/graphql"},
	}}
	record := snapshotExperiment(0, draft, "", request.Execution{})
	if record.Draft.GraphQL.Schema != "" || record.Draft.GraphQL.SchemaSource != "" {
		t.Fatalf("record kept the schema: %+v", record.Draft.GraphQL)
	}
	if record.Draft.GraphQL.Query != draft.GraphQL.Query || record.Draft.GraphQL.OperationName != "Q" {
		t.Fatalf("record lost the authored body: %+v", record.Draft.GraphQL)
	}
}

func TestExperimentDraftsShareBaselineSchema(t *testing.T) {
	a := workspaceApp()
	big := strings.Repeat("x", 900<<10)
	a.addDocument(savedRequest{Name: "GraphQL", Method: "POST", URL: "https://api.example.com/graphql"}, "")
	d := a.requestDocument
	d.graphql.Schema, d.graphql.SchemaSource = big, "https://api.example.com/graphql"
	d.baseline.GraphQL.Schema, d.baseline.GraphQL.SchemaSource = big, d.graphql.SchemaSource
	drafts := a.experimentDrafts()
	if len(drafts) != 1 || !drafts[0].SchemaShared {
		t.Fatalf("drafts = %+v", drafts)
	}
	if drafts[0].Value.GraphQL.Schema != big || drafts[0].Baseline.GraphQL.Schema != "" || drafts[0].Baseline.GraphQL.SchemaSource != "" {
		t.Fatalf("baseline schema was duplicated: %+v", drafts[0])
	}
	d.graphql.Schema = big + "x"
	drafts = a.experimentDrafts()
	if drafts[0].SchemaShared || drafts[0].Baseline.GraphQL.Schema != big {
		t.Fatalf("a changed schema lost the baseline: %+v", drafts[0])
	}
	d.graphql.Schema, d.graphql.SchemaSource = big, "https://other.example.com/graphql"
	drafts = a.experimentDrafts()
	if drafts[0].SchemaShared || drafts[0].Baseline.GraphQL.Schema != big || drafts[0].Baseline.GraphQL.SchemaSource != "https://api.example.com/graphql" {
		t.Fatalf("a changed schema source lost the baseline: %+v", drafts[0])
	}
}

func TestRestoreExperimentDraft(t *testing.T) {
	draft := experimentDraft{
		Value:        savedRequest{Request: request.Request{GraphQL: request.GraphQLBody{Schema: "schema", SchemaSource: "source"}}},
		SchemaShared: true,
	}
	restored := restoreExperimentDraft(draft)
	if restored.Baseline.GraphQL.Schema != "schema" || restored.Baseline.GraphQL.SchemaSource != "source" {
		t.Fatalf("restored baseline = %+v", restored.Baseline.GraphQL)
	}
	draft.SchemaShared = false
	if restored := restoreExperimentDraft(draft); restored.Baseline.GraphQL.Schema != "" {
		t.Fatalf("an unshared baseline was invented: %+v", restored.Baseline.GraphQL)
	}
}

func TestExperimentLogRetainsLargeSchema(t *testing.T) {
	big := strings.Repeat("x", 900<<10)
	draft := savedRequest{Request: request.Request{GraphQL: request.GraphQLBody{Schema: big, SchemaSource: "https://api.example.com/graphql"}}}
	log := experimentLog{Version: experimentVersion, Drafts: []experimentDraft{{Value: draft}}}
	for range 100 {
		if err := log.retain(snapshotExperiment(0, draft, "", request.Execution{})); err != nil {
			t.Fatal(err)
		}
	}
	if len(log.Records) != 100 {
		t.Fatalf("retained %d records, want 100", len(log.Records))
	}
	if log.Records[0].Draft.GraphQL.Schema != "" {
		t.Fatal("a record kept a schema")
	}
}

func TestExperimentLogAcceptsLargeDraftSchemas(t *testing.T) {
	a := workspaceApp()
	big := strings.Repeat("y", 4<<20)
	for i := range 4 {
		a.addDocument(savedRequest{Name: fmt.Sprintf("Draft %d", i), Method: "POST", URL: "https://api.example.com/graphql"}, "")
		d := a.requestDocument
		d.graphql.Schema, d.graphql.SchemaSource = big, "https://api.example.com/graphql"
		d.baseline.GraphQL.Schema, d.baseline.GraphQL.SchemaSource = big, d.graphql.SchemaSource
	}
	drafts := a.experimentDrafts()
	if len(drafts) != 4 {
		t.Fatalf("drafts = %d", len(drafts))
	}
	for _, draft := range drafts {
		if !draft.SchemaShared || draft.Baseline.GraphQL.Schema != "" {
			t.Fatalf("baseline duplicated a 4 MiB schema: shared=%v", draft.SchemaShared)
		}
	}
	log := experimentLog{Version: experimentVersion, Drafts: drafts}
	if err := log.retain(snapshotExperiment(0, savedRequest{}, "", request.Execution{})); err != nil {
		t.Fatalf("retain with four 4 MiB drafts: %v", err)
	}
	if len(log.Records) != 1 {
		t.Fatalf("records = %d", len(log.Records))
	}
}

func TestLoadExperimentsRestoresSharedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.json")
	value := savedRequest{Request: request.Request{
		ID: "draft", Name: "GraphQL", Method: "POST", URL: "https://api.example.com/graphql",
		BodyMode: "GraphQL", GraphQL: request.GraphQLBody{Query: "query Q { viewer { id } }", Schema: "type Query { viewer: User }", SchemaSource: "https://api.example.com/graphql"},
	}}
	log := experimentLog{Version: experimentVersion, Drafts: []experimentDraft{{
		Key: "draft-key", Value: value, Baseline: value, SchemaShared: true,
	}}}
	// The baseline is stored without the schema, as checkpointExperiments does.
	log.Drafts[0].Baseline.GraphQL.Schema, log.Drafts[0].Baseline.GraphQL.SchemaSource = "", ""
	if err := saveExperiments(path, log); err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	a.loadExperiments(path)
	if a.requestDocument == nil {
		t.Fatal("draft was not loaded")
	}
	if a.requestDocument.graphql.Schema != value.GraphQL.Schema {
		t.Fatalf("draft schema = %q", a.requestDocument.graphql.Schema)
	}
	if a.requestDocument.baseline.GraphQL.Schema != value.GraphQL.Schema || a.requestDocument.baseline.GraphQL.SchemaSource != value.GraphQL.SchemaSource {
		t.Fatalf("baseline schema was not restored: %+v", a.requestDocument.baseline.GraphQL)
	}
	if a.requestDocument.dirty() {
		t.Fatal("a restored shared schema marked the draft edited")
	}
}
