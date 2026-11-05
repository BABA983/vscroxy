package request

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Send applies each auth's header on the real Execute path, after scripts
// and the body are final.
func TestAuthHeadersReachTheServer(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	srv := testProxy(t)
	for _, tt := range []struct {
		name string
		auth Auth
		want string
	}{
		{"oauth1", Auth{Type: AuthOAuth1, OAuth1: OAuth1Auth{
			ConsumerKey: "ck", ConsumerSecret: "cs", Token: "token", TokenSecret: "ts",
			Timestamp: "137131201", Nonce: "7d8f3e4a",
		}}, `OAuth oauth_consumer_key="ck"`},
		{"hawk", Auth{Type: AuthHawk, Hawk: HawkAuth{
			AuthID: "dh37fgj492je", AuthKey: "werxhqb98rpaxn39848xrunpaw3489ruxnpa98w4rxn",
			Timestamp: "1353832234", Nonce: "j4h3g2",
		}}, `Hawk id="dh37fgj492je"`},
		{"edgegrid", Auth{Type: AuthEdgeGrid, EdgeGrid: EdgeGridAuth{
			AccessToken: "at", ClientToken: "ct", ClientSecret: "cs",
			Timestamp: "20160804T07:00:00+0000", Nonce: "ec9d20ee-1e9b-4c1f-925a-f0017754f86c",
		}}, `EG1-HMAC-SHA256 client_token=ct;access_token=at`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := Request{Method: "GET", URL: upstream.URL + "/me?x=1", Auth: tt.auth}
			out := r.Execute(t.Context(), srv, Context{}, nil, nil)
			if out.Err != nil || out.Response == nil || out.Response.Status != 200 {
				t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
			}
			mu.Lock()
			got := seen[len(seen)-1]
			mu.Unlock()
			if !strings.HasPrefix(got, tt.want) {
				t.Errorf("server saw %q, want a prefix of %q", got, tt.want)
			}
		})
	}
}
