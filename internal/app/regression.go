package app

import (
	"github.com/baba983/vscroxy/locales"
	"strconv"
	// regressionFrom creates an ordinary checked request. Collections and Runner
	// own it; sharing uses the collection's Postman export, not a custom format.
)

func (a *app) regressionFrom(f flow) {
	r, err := f.requestDraft()
	if err != nil {
		a.fail(locales.SaveRegression(), err)
		return
	}
	r.Source = newDebugSource(f)
	r.Name = uniqueRequestName(a.requests, f.method+" "+hostOnly(f.host)+" regression")
	r.Collection, r.Checks.Status = "Regressions", strconv.Itoa(f.status)
	a.addDocument(r, "")
	a.sourceFlow, a.composeTab = f.id, composeTabChecks
	a.inform(locales.ReviewChecksAndVariablesThen())
}
