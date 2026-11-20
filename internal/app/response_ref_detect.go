package app

import (
	"context"
	"errors"
	"mime"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// These are suggestions only. Nothing here writes a request, a variable, a
// flow or a log: the preview holds secret values in memory until it closes,
// and the UI shows a masked length instead of the matched token.
type responseRefConsumer struct {
	Flow   int
	Field  string // Bearer, Header, Query or Body
	Name   string // header or query name, when there is one
	Source int    // source flow ID, kept with the suggestion
}

type responseRefSuggestion struct {
	Source    int
	Path      []string
	Consumers []responseRefConsumer
	token     string
}

type responseRefAnalysis struct {
	Suggestions []responseRefSuggestion
	Skipped     int
}

// recognizeResponseRefs finds a value in one response's JSON that reappears
// in a later request's Bearer auth, another header, a query parameter or the
// body. It is read-only inference, never proof of an authentication chain.
// Short or coincidental strings are not candidates: a source field must be
// named like a credential and hold a token-shaped value of at least 16
// characters, and the match must be unique across the selected flows.
func recognizeResponseRefs(ctx context.Context, flows []flow) (responseRefAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return responseRefAnalysis{}, err
	}
	if len(flows) == 0 || len(flows) > tokenSelectionLimit {
		return responseRefAnalysis{}, errors.New("select 1–200 unique positive HTTP flow IDs")
	}
	ordered := slices.Clone(flows)
	slices.SortFunc(ordered, func(a, b flow) int { return a.id - b.id })
	var suggestions []responseRefSuggestion
	remaining := tokenReadLimit
	result := responseRefAnalysis{Suggestions: []responseRefSuggestion{}}
	for _, f := range ordered {
		if err := ctx.Err(); err != nil {
			return responseRefAnalysis{}, err
		}
		ct, count := tokenHeader(f.respHeader, f.resHeaders, "Content-Type")
		media, _, err := mime.ParseMediaType(ct)
		isJSON := media == "application/json" || strings.HasSuffix(media, "+json")
		if f.at.IsZero() || f.id <= 0 || f.status < 200 || f.status >= 300 || count != 1 || err != nil || !isJSON {
			result.Skipped++
			continue
		}
		body, ok := tokenResponseBody(ctx, f, &remaining)
		if !ok {
			result.Skipped++
			continue
		}
		fields, ok := tokenResponseFields(ctx, body, f.id)
		if !ok {
			result.Skipped++
			continue
		}
		for _, field := range fields {
			if validInferredToken(field.token) {
				suggestions = append(suggestions, responseRefSuggestion{Source: f.id, Path: field.Path, token: field.token})
			}
		}
	}
	for _, consumer := range ordered {
		if err := ctx.Err(); err != nil {
			return responseRefAnalysis{}, err
		}
		if consumer.at.IsZero() || consumer.id <= 0 || consumer.kind != "" {
			continue
		}
		body, hasBody := responseRefReadBody(ctx, consumer, &remaining)
		matches, chosen, index := 0, responseRefConsumer{}, -1
		for i := range suggestions {
			for _, place := range responseRefConsumerPlaces(consumer, body, hasBody, suggestions[i].token) {
				matches++
				chosen, index = place, i
			}
		}
		if matches != 1 {
			continue // no match, or more than one eligible value: never guess
		}
		if src := slices.IndexFunc(ordered, func(f flow) bool { return f.id == suggestions[index].Source }); src >= 0 {
			if !ordered[src].at.Before(consumer.at) || ordered[src].id == consumer.id {
				continue
			}
		}
		chosen.Source, chosen.Flow = suggestions[index].Source, consumer.id
		suggestions[index].Consumers = append(suggestions[index].Consumers, chosen)
	}
	for _, s := range suggestions {
		if len(s.Consumers) > 0 {
			result.Suggestions = append(result.Suggestions, s)
		}
	}
	slices.SortFunc(result.Suggestions, func(a, b responseRefSuggestion) int {
		return a.Source - b.Source
	})
	if err := ctx.Err(); err != nil {
		return responseRefAnalysis{}, err
	}
	return result, nil
}

// responseRefReadBody reads a retained request body under the same budget as
// the token detector uses for a response. Spilled files are read from disk;
// only the app's own capture files are ever passed here.
func responseRefReadBody(ctx context.Context, f flow, remaining *int) (string, bool) {
	return tokenResponseBody(ctx, flow{resBody: f.reqBody, resFile: f.reqFile, resSize: f.reqSize, resError: f.reqError}, remaining)
}

// responseRefConsumerPlaces lists the distinct authoring fields that carry
// token exactly once. Repeated equal matches are deliberately not a single
// unambiguous place, so a generic substring cannot silently be rewritten.
func responseRefConsumerPlaces(f flow, body string, hasBody bool, token string) []responseRefConsumer {
	var places []responseRefConsumer
	for _, pair := range headerPairs(f.reqHeaders, f.reqHeader) {
		field, value := "Header", strings.TrimSpace(pair.Value)
		if scheme, rest, ok := strings.Cut(value, " "); ok && strings.EqualFold(scheme, "Bearer") {
			field, value = "Bearer", strings.TrimLeft(rest, " ")
		}
		if strings.Count(value, token) != 1 || !validInferredToken(value) {
			continue
		}
		places = append(places, responseRefConsumer{Field: field, Name: pair.Name})
	}
	raw := f.url
	if raw == "" && f.scheme != "" && f.host != "" {
		raw = f.scheme + "://" + f.host + f.path
	}
	if u, err := url.Parse(raw); err == nil {
		for key, values := range u.Query() {
			for _, value := range values {
				if strings.Count(value, token) == 1 && validInferredToken(value) {
					places = append(places, responseRefConsumer{Field: "Query", Name: key})
				}
			}
		}
	}
	if hasBody && len(body) <= tokenResponseLimit && strings.Count(body, token) == 1 {
		places = append(places, responseRefConsumer{Field: "Body"})
	}
	return places
}

// responseRefExpression is how the reference is written into a consumer.
func responseRefExpression(name string, path []string) string {
	return `{{response ` + strconv.Quote(name) + ` ` + strconv.Quote(responseRefPointer(path)) + `}}`
}
