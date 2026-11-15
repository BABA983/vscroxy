// Package update asks GitHub for the latest vscroxy release. It only reads a
// version and a page URL: nothing is downloaded or installed, and the request
// carries no information about this computer.
package update

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Repository is the project whose releases are checked.
const Repository = "BABA983/vscroxy"

// LatestURL is the GitHub API endpoint for the newest published release.
// GitHub leaves out drafts and prereleases.
const LatestURL = "https://api.github.com/repos/" + Repository + "/releases/latest"

// releaseSizeLimit bounds what a response may make the app read.
const releaseSizeLimit = 1 << 20

// Release is the part of a GitHub release the app uses.
type Release struct {
	Tag string
	URL string
}

// Latest asks GitHub for the newest release. The caller's context decides how
// long the request may take.
func Latest(ctx context.Context, client *http.Client) (Release, error) {
	return latestAt(ctx, client, LatestURL)
}

func latestAt(ctx context.Context, client *http.Client, url string) (Release, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	// GitHub asks for a User-Agent; this one names the app and its purpose.
	req.Header.Set("User-Agent", "vscroxy-update-check")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, releaseSizeLimit+1))
	if err != nil {
		return Release{}, err
	}
	if len(data) > releaseSizeLimit {
		return Release{}, fmt.Errorf("release response exceeds %d bytes", releaseSizeLimit)
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Draft   bool   `json:"draft"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return Release{}, err
	}
	if body.TagName == "" || body.Draft {
		return Release{}, fmt.Errorf("no published release")
	}
	return Release{Tag: body.TagName, URL: body.HTMLURL}, nil
}

// Newer reports whether latest is a later version than current. Both are
// release numbers such as "0.1.0" or "v0.2.1"; a prerelease of the same
// number is older than the release.
func Newer(current, latest string) bool {
	c, cpre, ok := parse(current)
	if !ok {
		return false
	}
	l, lpre, ok := parse(latest)
	if !ok {
		return false
	}
	for i := range c {
		switch {
		case l[i] > c[i]:
			return true
		case l[i] < c[i]:
			return false
		}
	}
	return cpre && !lpre
}

// parse reads the numbers of a version and whether it is a prerelease.
func parse(v string) (numbers [3]int, prerelease bool, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		prerelease, v = true, v[:i]
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return numbers, false, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return numbers, false, false
		}
		numbers[i] = n
	}
	return numbers, prerelease, true
}
