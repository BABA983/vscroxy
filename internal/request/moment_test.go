package request

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

// momentZones are the fixed zones the expectations were generated for. goja's
// Date reads Go's time.Local, so each subtest sets the process zone to the
// same IANA zone the Node run used.
var momentZones = []string{"UTC", "Asia/Shanghai", "America/New_York"}

// momentEvery is a value the same in every tested zone.
func momentEvery(value string) map[string]string {
	return map[string]string{"UTC": value, "Asia/Shanghai": value, "America/New_York": value}
}

// momentCases were generated with Node v26.12.1 and the unmodified
// moment@2.31.0 min build (moment/min/moment.min), the latest 2.x release in
// the line Postman's sandbox pins (2.30.1), not the full package with its
// locales. For each zone:
//
//	TZ=<zone> node -e 'const moment = require("moment/min/moment.min");
//	  console.log(String(<expression>))'
//
// The expressions below are copied from that run, including the DST dates in
// America/New_York (2024-03-10 and 2024-11-03). The one case Node and goja
// disagree on, a wall time inside the spring-forward gap, is checked
// separately by TestMomentSpringForwardGapFollowsGo.
var momentCases = []struct {
	name, expr string
	want       map[string]string
}{
	{"version", "moment.version", momentEvery("2.31.0")},
	{"local format from a timestamp", "moment(1706743845123).format('YYYY-MM-DD HH:mm:ss.SSS Z')", map[string]string{
		"UTC": "2024-01-31 23:30:45.123 +00:00", "Asia/Shanghai": "2024-02-01 07:30:45.123 +08:00", "America/New_York": "2024-01-31 18:30:45.123 -05:00"}},
	{"ISO with an offset to UTC", "moment('2024-01-31T23:30:45+08:00').toISOString()", momentEvery("2024-01-31T15:30:45.000Z")},
	{"ISO with Z formatted locally", "moment('2024-01-31T23:30:45Z').format()", map[string]string{
		"UTC": "2024-01-31T23:30:45+00:00", "Asia/Shanghai": "2024-02-01T07:30:45+08:00", "America/New_York": "2024-01-31T18:30:45-05:00"}},
	{"ISO without an offset is local", "moment('2024-01-31T23:30:45').toISOString()", map[string]string{
		"UTC": "2024-01-31T23:30:45.000Z", "Asia/Shanghai": "2024-01-31T15:30:45.000Z", "America/New_York": "2024-02-01T04:30:45.000Z"}},
	{"utc from a timestamp", "moment.utc(1706743845123).format()", momentEvery("2024-01-31T23:30:45Z")},
	{"utc ISO default format", "moment.utc('2024-01-31T23:30:45.123Z').format()", momentEvery("2024-01-31T23:30:45Z")},
	{"add a month clamps to month end", "moment('2024-01-31T12:00:00Z').add(1,'month').format('YYYY-MM-DD')", momentEvery("2024-02-29")},
	{"subtract a month clamps to month end", "moment('2024-03-31T12:00:00Z').subtract(1,'month').format('YYYY-MM-DD')", momentEvery("2024-02-29")},
	{"diff in days", "moment('2024-03-01T00:00:00Z').diff(moment('2024-02-01T00:00:00Z'), 'days')", momentEvery("29")},
	{"startOf month in UTC", "moment.utc('2024-02-15T13:45:30.500Z').startOf('month').toISOString()", momentEvery("2024-02-01T00:00:00.000Z")},
	{"endOf month in UTC", "moment.utc('2024-02-15T13:45:30.500Z').endOf('month').toISOString()", momentEvery("2024-02-29T23:59:59.999Z")},
	{"custom format strict", "moment('31/01/2024 23:30', 'DD/MM/YYYY HH:mm', true).toISOString()", map[string]string{
		"UTC": "2024-01-31T23:30:00.000Z", "Asia/Shanghai": "2024-01-31T15:30:00.000Z", "America/New_York": "2024-02-01T04:30:00.000Z"}},
	{"strict rejects an impossible date", "moment('2024-02-30', 'YYYY-MM-DD', true).isValid()", momentEvery("false")},
	{"an impossible date is invalid", "moment('2024-02-30', 'YYYY-MM-DD').format('YYYY-MM-DD')", momentEvery("Invalid date")},
	{"add a day keeps the wall clock across spring DST", "moment('2024-03-09T12:00:00').add(1,'day').format('YYYY-MM-DD HH:mm:ss Z')", map[string]string{
		"UTC": "2024-03-10 12:00:00 +00:00", "Asia/Shanghai": "2024-03-10 12:00:00 +08:00", "America/New_York": "2024-03-10 12:00:00 -04:00"}},
	{"add 24 hours is absolute time across spring DST", "moment('2024-03-09T12:00:00').add(24,'hours').format('YYYY-MM-DD HH:mm:ss Z')", map[string]string{
		"UTC": "2024-03-10 12:00:00 +00:00", "Asia/Shanghai": "2024-03-10 12:00:00 +08:00", "America/New_York": "2024-03-10 13:00:00 -04:00"}},
	{"diff in hours across spring DST", "moment('2024-03-10T12:00:00').diff(moment('2024-03-09T12:00:00'), 'hours')", map[string]string{
		"UTC": "24", "Asia/Shanghai": "24", "America/New_York": "23"}},
	{"subtract a day keeps the wall clock across fall DST", "moment('2024-11-03T12:00:00').subtract(1,'day').format('YYYY-MM-DD HH:mm:ss Z')", map[string]string{
		"UTC": "2024-11-02 12:00:00 +00:00", "Asia/Shanghai": "2024-11-02 12:00:00 +08:00", "America/New_York": "2024-11-02 12:00:00 -04:00"}},
	{"startOf day is local midnight", "moment('2024-03-09T12:00:00').startOf('day').toISOString()", map[string]string{
		"UTC": "2024-03-09T00:00:00.000Z", "Asia/Shanghai": "2024-03-08T16:00:00.000Z", "America/New_York": "2024-03-09T05:00:00.000Z"}},
	{"endOf day is local", "moment('2024-03-10T12:00:00').endOf('day').toISOString()", map[string]string{
		"UTC": "2024-03-10T23:59:59.999Z", "Asia/Shanghai": "2024-03-10T15:59:59.999Z", "America/New_York": "2024-03-11T03:59:59.999Z"}},
	{"isBefore", "moment('2024-03-09T12:00:00').isBefore('2024-03-10T12:00:00')", momentEvery("true")},
	{"the repeated fall-back hour keeps the earlier offset", "moment('2024-11-03T01:30:00').format('YYYY-MM-DD HH:mm:ss Z')", map[string]string{
		"UTC": "2024-11-03 01:30:00 +00:00", "Asia/Shanghai": "2024-11-03 01:30:00 +08:00", "America/New_York": "2024-11-03 01:30:00 -04:00"}},
	{"the repeated fall-back hour to UTC", "moment('2024-11-03T01:30:00').toISOString()", map[string]string{
		"UTC": "2024-11-03T01:30:00.000Z", "Asia/Shanghai": "2024-11-02T17:30:00.000Z", "America/New_York": "2024-11-03T05:30:00.000Z"}},
	{"default locale", "moment.locale()", momentEvery("en")},
	{"no Chinese locale is bundled", "moment.locale('zh-cn')", momentEvery("en")},
	{"no UK locale is bundled", "moment.locale('en-gb')", momentEvery("en")},
	{"English weekdays", "moment.weekdays(3)", momentEvery("Wednesday")},
	{"English months", "moment.months(1)", momentEvery("February")},
	{"default format", "moment.defaultFormat", momentEvery("YYYY-MM-DDTHH:mm:ssZ")},
}

// momentResults runs every expression in one request script and returns what
// String() made of each, so require("moment") is exercised the same way a
// pre-request or post-response script uses it.
func momentResults(t *testing.T, zone string) []string {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	previous := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = previous })

	var script strings.Builder
	script.WriteString("const moment = require(\"moment\");\nconst out = [];\n")
	for _, c := range momentCases {
		fmt.Fprintf(&script, "out.push(String(%s));\n", c.expr)
	}
	script.WriteString("pm.environment.set(\"out\", JSON.stringify(out));")
	run := &scriptRun{vars: map[string]string{}}
	if err := runScript(script.String(), run, nil); err != nil {
		t.Fatalf("%s: %v", zone, err)
	}
	var got []string
	if err := json.Unmarshal([]byte(run.vars["out"]), &got); err != nil {
		t.Fatalf("%s: results %q: %v", zone, run.vars["out"], err)
	}
	if len(got) != len(momentCases) {
		t.Fatalf("%s: %d results, want %d", zone, len(got), len(momentCases))
	}
	return got
}

// TestMomentMatchesNode compares parse, format, arithmetic, diff, boundaries
// and the bundled locale with the same moment build under Node.
func TestMomentMatchesNode(t *testing.T) {
	for _, zone := range momentZones {
		t.Run(zone, func(t *testing.T) {
			got := momentResults(t, zone)
			for i, c := range momentCases {
				if want := c.want[zone]; got[i] != want {
					t.Errorf("%s: %s = %q, want %q", c.name, c.expr, got[i], want)
				}
			}
		})
	}
}

// TestMomentUsesTheGoProcessLocalZone proves moment's local offset is Go's
// time.Local, not a fixed zone and not UTC.
func TestMomentUsesTheGoProcessLocalZone(t *testing.T) {
	for _, zone := range momentZones {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Fatal(err)
			}
			previous := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = previous })

			run := &scriptRun{vars: map[string]string{}}
			if err := runScript(`const moment = require("moment");
const now = moment();
pm.environment.set("momentOffset", String(now.utcOffset()));
pm.environment.set("dateOffset", String(-new Date().getTimezoneOffset()));
pm.environment.set("offsetString", now.format("Z"));
pm.environment.set("delta", String(Math.abs(now.valueOf() - Date.now())));`, run, nil); err != nil {
				t.Fatal(err)
			}
			_, offset := time.Now().Zone()
			want := strconv.Itoa(offset / 60)
			if run.vars["momentOffset"] != want || run.vars["dateOffset"] != want {
				t.Errorf("moment offset %q, Date offset %q, Go zone offset %q", run.vars["momentOffset"], run.vars["dateOffset"], want)
			}
			sign := "+"
			if offset < 0 {
				sign, offset = "-", -offset
			}
			wantZ := fmt.Sprintf("%s%02d:%02d", sign, offset/3600, offset/60%60)
			if run.vars["offsetString"] != wantZ {
				t.Errorf("moment().format(Z) = %q, want %q", run.vars["offsetString"], wantZ)
			}
			if delta, _ := strconv.Atoi(run.vars["delta"]); delta > 1000 {
				t.Errorf("moment() is %d ms from Date.now()", delta)
			}
		})
	}
}

// TestMomentSpringForwardGapFollowsGo pins the one known difference from
// Node. Go's time.Date resolves a wall clock inside a spring-forward gap to
// the offset before the transition, so 02:30 in America/New_York becomes
// 01:30 -05:00. Node/V8 resolves it to 03:30 -04:00. The embedded moment is
// unmodified and goja builds its Date through Go, so this follows Go.
func TestMomentSpringForwardGapFollowsGo(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	previous := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = previous })

	run := &scriptRun{vars: map[string]string{}}
	if err := runScript(`const moment = require("moment");
pm.environment.set("gap", moment('2024-03-10T02:30:00').format('YYYY-MM-DD HH:mm:ss Z'));`, run, nil); err != nil {
		t.Fatal(err)
	}
	// Node v26.12.1 with moment 2.31.0 says "2024-03-10 03:30:00 -04:00".
	if got, want := run.vars["gap"], "2024-03-10 01:30:00 -05:00"; got != want {
		t.Errorf("spring-forward gap = %q, want Go's normalization %q (Node says 2024-03-10 03:30:00 -04:00)", got, want)
	}
}

// TestMomentStateIsPerScript checks the module is cached within one script,
// that its global settings change that script, and that the next script and
// the next run start from the defaults.
func TestMomentStateIsPerScript(t *testing.T) {
	pre := `const first = require("moment");
const second = require("moment");
if (first !== second || pm.require("moment") !== first) throw new Error("require must return one module per script");
if (first.locale() !== "en" || first.months(1) !== "February" || first.defaultFormat !== "YYYY-MM-DDTHH:mm:ssZ") {
  throw new Error("a script did not start from the defaults");
}
first.updateLocale("en", {months: ["JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"]});
first.defaultFormat = "[mutated]";
pm.environment.set("pre", first.months(0) + "|" + first.defaultFormat);`
	post := `const moment = require("moment");
pm.environment.set("post", moment.months(1) + "|" + moment.defaultFormat + "|" + moment.locale());`
	for run := 1; run <= 2; run++ {
		before := &scriptRun{vars: map[string]string{}}
		if err := runScript(pre, before, nil); err != nil {
			t.Fatalf("run %d pre-request: %v", run, err)
		}
		after := &scriptRun{vars: map[string]string{}}
		if err := runScript(post, after, nil); err != nil {
			t.Fatalf("run %d post-response: %v", run, err)
		}
		if got, want := before.vars["pre"], "JAN|[mutated]"; got != want {
			t.Errorf("run %d pre-request saw %q, want %q", run, got, want)
		}
		if got, want := after.vars["post"], "February|YYYY-MM-DDTHH:mm:ssZ|en"; got != want {
			t.Errorf("run %d post-response saw %q, want fresh defaults %q", run, got, want)
		}
	}
}

// TestMomentInRuleScriptsAddonsAndMocks covers an ES module importing moment
// directly and through an addon, and a Code Mock's onMock.
func TestMomentInRuleScriptsAddonsAndMocks(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	previous := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = previous })

	addons := map[string]string{
		"clock": "import moment from \"moment\";\nexport const stamp = (iso) => moment.utc(iso).format(\"YYYY-MM-DD HH:mm:ss\");\n",
	}
	s, err := ParseProxyScript("Clock", `import moment from "moment";
import { stamp } from "clock";

export function onRequest() {
  pm.request.headers.upsert("X-Local", moment("2024-01-31T23:30:45Z").format("YYYY-MM-DD HH:mm:ss Z"));
  pm.request.headers.upsert("X-UTC", stamp("2024-01-31T23:30:45.123Z"));
}

export function onMock() {
  pm.response.setBody({at: moment.utc("2024-01-31T23:30:45Z").add(1, "day").format()});
}`)
	if err != nil {
		t.Fatal(err)
	}
	request := &capture.ScriptExchange{Phase: "request", Rule: "Clock", Method: "GET", URL: "https://api.test/", Header: http.Header{}}
	if _, err := s.Run(t.Context(), request, ProxyEnv{Addons: addons}); err != nil {
		t.Fatal(err)
	}
	if got, want := request.Header.Get("X-Local"), "2024-02-01 07:30:45 +08:00"; got != want {
		t.Errorf("rule script X-Local = %q, want %q", got, want)
	}
	if got, want := request.Header.Get("X-UTC"), "2024-01-31 23:30:45"; got != want {
		t.Errorf("addon X-UTC = %q, want %q", got, want)
	}

	mock := &capture.ScriptExchange{Phase: "mock", Rule: "Clock", Method: "GET", URL: "https://api.test/", Header: http.Header{}}
	if _, err := s.Run(t.Context(), mock, ProxyEnv{Addons: addons}); err != nil {
		t.Fatal(err)
	}
	if mock.Status != http.StatusOK || string(mock.ResponseBody) != `{"at":"2024-02-01T23:30:45Z"}` {
		t.Errorf("mock = %d %q", mock.Status, mock.ResponseBody)
	}
}
