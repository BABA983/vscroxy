package request

import (
	"fmt"
	"strings"
)

// wgetSnippet writes a wget 1.20+ command. It prints the response body to
// stdout and leaves redirects off, like the app's sends.
func wgetSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	if method == "HEAD" && (req.Body != "" || req.BodyFile != "") {
		return "", fmt.Errorf("wget cannot send a body with HEAD; copy it as cURL instead")
	}
	var args []string
	args = append(args, "wget", "-qO-", "--content-on-error", "--max-redirect=0")
	if method != "GET" || req.Body != "" || req.BodyFile != "" {
		args = append(args, "--method="+shellQuote(method))
	}
	for _, h := range headers {
		values, err := snippetHeaderValues(h, "wget")
		if err != nil {
			return "", err
		}
		for _, value := range values {
			if value == "" {
				args = append(args, "--header="+shellQuote(h.name+":"))
			} else {
				args = append(args, "--header="+shellQuote(h.name+": "+value))
			}
		}
	}
	switch {
	case req.BodyFile != "":
		args = append(args, "--body-file="+shellQuote(req.BodyFile))
	case req.Body != "":
		args = append(args, "--body-data="+shellQuote(req.Body))
	}
	args = append(args, shellQuote(req.URL))
	return strings.Join(args, " \\\n  ") + "\n", nil
}
