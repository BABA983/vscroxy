package app

import (
	"testing"
)

func TestSendContextResolvesRunRequestCalls(t *testing.T) {
	a := workspaceApp()
	in := a.sendContext(a.requests[0].Request)
	if in.Resolve == nil {
		t.Fatal("a send cannot call pm.execution.runRequest")
	}
	callee, err := in.Resolve("List projects")
	if err != nil || callee.Request.Collection != "Workspace/API" {
		t.Errorf("by name: %+v, %v", callee.Request, err)
	}
	if _, err := in.Resolve("Nope"); err == nil {
		t.Error("an unknown name resolved")
	}
}
