package app

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/openapi"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/getkin/kin-openapi/openapi3"
)

// contractResult is one exchange checked against every saved contract.
type contractResult struct {
	Checked    int
	Drifts     []openapi.Drift
	Failures   []string
	NotCovered bool
}

// contractSpecsKey changes whenever a saved contract changes, so cached
// checks cannot outlive the document they used.
func (a *app) contractSpecsKey() string {
	hash := sha256.New()
	for _, doc := range a.apis {
		if doc.saved == nil {
			continue
		}
		spec := *doc.saved
		fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%s\x00", spec.ID, spec.Path, spec.Fingerprint, spec.Source)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

// contractCheck validates ex against every saved contract and caches the
// result by exchange identity and contract contents.
func (a *app) contractCheck(key string, ex openapi.Exchange) contractResult {
	cacheKey := a.contractSpecsKey() + "|" + key
	if result, ok := a.contractCache[cacheKey]; ok {
		return result
	}
	var result contractResult
	for _, api := range a.apis {
		if api.saved == nil {
			continue
		}
		spec := *api.saved
		var doc *openapi3.T
		var err error
		if spec.Path != "" {
			var loaded *openapi.Spec
			loaded, err = openapi.LoadSource(spec.Path, []byte(spec.Source))
			if err == nil {
				doc = loaded.Doc
			}
		} else {
			doc, err = parseOpenAPI(spec.Source)
		}
		if err != nil {
			name := spec.ID
			if spec.Path != "" {
				name = filepath.Base(spec.Path)
			}
			result.Failures = append(result.Failures, locales.ContractSpecFailed(name, err.Error()))
			continue
		}
		checker, err := openapi.NewChecker(doc)
		if err != nil {
			result.Failures = append(result.Failures, locales.ContractSpecFailed(spec.ID, err.Error()))
			continue
		}
		result.Checked++
		if drift, matched := checker.Check(ex); matched {
			result.Drifts = append(result.Drifts, drift)
		}
	}
	result.NotCovered = len(result.Drifts) == 0
	if a.contractCache == nil || len(a.contractCache) > 256 {
		a.contractCache = map[string]contractResult{}
	}
	a.contractCache[cacheKey] = result
	return result
}

// flowExchange turns a captured row into the contract checker's input.
func (a *app) flowExchange(f flow) openapi.Exchange {
	reqBody, resBody := []byte(f.reqBody), []byte(f.resBody)
	if f.reqFile != "" {
		reqBody = readContractBody(f.reqFile)
	}
	if f.resFile != "" {
		resBody = readContractBody(f.resFile)
	}
	return openapi.Exchange{
		Method: f.method, URL: f.url, Header: f.reqHeader, Body: reqBody,
		Status: f.status, ResponseHeader: f.respHeader, ResponseBody: resBody,
	}
}

// captureExchange turns a sent response into the contract checker's input.
func (a *app) captureExchange(r *capture.Flow) openapi.Exchange {
	reqBody, resBody := []byte(r.RequestBody), []byte(r.ResponseBody)
	if r.RequestBodyFile != "" {
		reqBody = readContractBody(r.RequestBodyFile)
	}
	if r.ResponseBodyFile != "" {
		resBody = readContractBody(r.ResponseBodyFile)
	}
	return openapi.Exchange{
		Method: r.Method, URL: r.URL, Header: r.RequestHeader, Body: reqBody,
		Status: r.Status, ResponseHeader: r.ResponseHeader, ResponseBody: resBody,
	}
}

// readContractBody reads a spilled body within a bound. The contract view runs
// on the UI thread, so it reads once per exchange and the result is cached.
func readContractBody(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16<<20))
	if err != nil {
		return nil
	}
	return data
}

// contractView shows one exchange's drift against every saved contract.
func (a *app) contractView(c *ui.Context, t *ui.Theme, key string, ex openapi.Exchange) {
	if !slices.ContainsFunc(a.apis, func(d *apiDocument) bool { return d.saved != nil }) {
		uikit.Hint(c, locales.ContractNoSpecs()).Padding(t.Space(3))
		return
	}
	result := a.contractCheck(key, ex)
	ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Children(func() {
		ui.Column(c).Gap(t.Space(2)).Children(func() {
			if result.Checked > 0 {
				uikit.Meta(c, locales.ContractChecked(result.Checked))
			}
			for _, failure := range result.Failures {
				ui.Text(c, failure).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Warning).Selectable()
			}
			if result.NotCovered {
				uikit.Hint(c, locales.ContractNotCovered())
				return
			}
			clean := true
			for _, drift := range result.Drifts {
				if len(drift.Problems) > 0 {
					clean = false
				}
			}
			if clean {
				uikit.Body(c, locales.ContractMatches()).TextColor(t.Success)
			}
			for _, drift := range result.Drifts {
				if len(drift.Problems) == 0 {
					continue
				}
				uikit.Mono(c, drift.Endpoint).FontWeight(500)
				if len(drift.Servers) > 0 {
					uikit.Meta(c, locales.ContractMatchedByPath(drift.MatchedBase, strings.Join(drift.Servers, ", ")))
				}
				uikit.Meta(c, locales.ContractDriftCount(len(drift.Problems)))
				for _, problem := range drift.Problems {
					ui.Text(c, "• "+problem).FontSize(uikit.Scaled(uikit.TextSmall)).TextColor(t.Danger).Selectable()
				}
			}
		})
	})
}
