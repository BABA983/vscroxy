package request

import (
	"fmt"
	"strings"
)

// guzzleSnippet writes a PHP Guzzle request. The autoloader is expected to be
// installed by Composer.
func guzzleSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	var b strings.Builder
	b.WriteString("<?php\n")
	b.WriteString("require 'vendor/autoload.php';\n\n")
	b.WriteString("$client = new \\GuzzleHttp\\Client([\n")
	b.WriteString("    'allow_redirects' => false,\n")
	b.WriteString("    'http_errors' => false,\n")
	b.WriteString("]);\n")
	b.WriteString("$response = $client->request(" + phpQuote(method) + ", " + phpQuote(req.URL) + ", [\n")
	if len(headers) > 0 {
		b.WriteString("    'headers' => [\n")
		for _, h := range headers {
			if strings.EqualFold(h.name, "Host") {
				return "", fmt.Errorf("PHP Guzzle cannot set the Host header; copy it as cURL instead")
			}
			values, err := snippetHeaderValues(h, "PHP Guzzle")
			if err != nil {
				return "", err
			}
			if len(values) == 1 {
				b.WriteString("        " + phpQuote(h.name) + " => " + phpQuote(values[0]) + ",\n")
			} else {
				b.WriteString("        " + phpQuote(h.name) + " => [")
				for i, value := range values {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(phpQuote(value))
				}
				b.WriteString("],\n")
			}
		}
		b.WriteString("    ],\n")
	}
	switch {
	case req.BodyFile != "":
		b.WriteString("    'body' => fopen(" + phpQuote(req.BodyFile) + ", 'rb'),\n")
	case req.Body != "":
		b.WriteString("    'body' => " + phpQuote(req.Body) + ",\n")
	}
	b.WriteString("]);\n")
	b.WriteString("echo $response->getStatusCode() . \"\\n\";\n")
	b.WriteString("echo $response->getBody();\n")
	return b.String(), nil
}
