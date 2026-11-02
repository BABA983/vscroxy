package hosts

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"time"
)

// maxFetch bounds a subscribed hosts file: public block lists run to a few
// megabytes, and a larger answer is not a hosts file.
const maxFetch = 8 << 20

// RefreshEvery is how old a subscribed profile may get before it is fetched
// again.
const RefreshEvery = time.Hour

// Fetch downloads a subscribed hosts file. It connects directly, not through
// a proxy the environment names, which may be this one.
func Fetch(ctx context.Context, source string) (string, error) {
	u, err := url.Parse(source)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("subscribe to an http(s) URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", u.Host, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetch+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxFetch {
		return "", fmt.Errorf("larger than 8 MiB; not a hosts file")
	}
	return string(data), nil
}

// SystemFile is where this system keeps its hosts file.
func SystemFile() string {
	if runtime.GOOS == "windows" {
		return cmp.Or(os.Getenv("SystemRoot"), `C:\Windows`) + `\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}
