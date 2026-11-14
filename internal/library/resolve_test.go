package library

import (
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func TestFindRequestPrefersIDAndRefusesSharedNames(t *testing.T) {
	requests := []request.Request{
		{ID: "a", Name: "Login", Collection: "Auth"},
		{ID: "b", Name: "Login", Collection: "Admin"},
		{ID: "Login", Name: "Ping", Collection: "Shop"},
		{ID: "c", Name: "Unique", Collection: "Shop"},
	}
	if got, err := FindRequest(requests, "c"); err != nil || got.Name != "Unique" {
		t.Errorf("by ID: %+v, %v", got, err)
	}
	// An ID wins over a name that equals it.
	if got, err := FindRequest(requests, "Login"); err != nil || got.ID != "Login" {
		t.Errorf("ID over name: %+v, %v", got, err)
	}
	if got, err := FindRequest(requests, "Unique"); err != nil || got.ID != "c" {
		t.Errorf("by unique name: %+v, %v", got, err)
	}
	if _, err := FindRequest(requests, "Ping"); err != nil {
		t.Errorf("a name that only matches once: %v", err)
	}
	if _, err := FindRequest(requests, "Nope"); err == nil {
		t.Error("an unknown name was found")
	}
	_, err := FindRequest(requests, "Shop")
	if err == nil || !strings.Contains(err.Error(), "no request") {
		t.Errorf("a collection name is not a request: %v", err)
	}
}
