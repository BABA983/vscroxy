package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/imports"
	"github.com/baba983/vscroxy/internal/request"
)

// importThirdParty merges one third-party export into the library. The
// reader has already converted it to requests, folders and environments;
// this owns the app's single library write and its report.
func (a *app) importThirdParty(result imports.Result, what string) {
	var incoming []savedRequest
	var incomingFolders []request.Folder
	var warnings []string
	warnings = append(warnings, result.Warnings...)
	for _, collection := range result.Collections {
		for _, r := range collection.Requests {
			incoming = append(incoming, savedRequest{Request: r})
		}
		incomingFolders = append(incomingFolders, collection.Folders...)
		warnings = append(warnings, collection.Warnings...)
	}
	if len(incoming) == 0 && len(result.Environments) == 0 {
		if len(warnings) == 0 {
			warnings = []string{"the export has no requests or environments"}
		}
		a.warn(what + ": nothing to import; " + strings.Join(warnings, "; "))
		return
	}
	warnings = uniqueWarnings(warnings)
	title := importTitle(result, len(incoming))
	if len(incoming) > 0 {
		merged := mergeRequests(a.requests, incoming)
		folders := mergeFolders(a.folders, incomingFolders)
		if err := a.writeLibrary(merged, folders); err != nil {
			a.fail(what, err)
			return
		}
		a.page = pageRequest
	}
	if len(result.Environments) > 0 {
		envTitle, envWarnings, err := a.mergeImportedEnvironments(result.Environments)
		if err != nil {
			// The requests are already saved; say what did not make it.
			warnings = append(warnings, "environments were not saved: "+err.Error())
		} else if envTitle != "" && len(incoming) == 0 {
			title = envTitle
		}
		warnings = append(warnings, envWarnings...)
	}
	a.reportImport(title, uniqueWarnings(warnings))
}

// importTitle names what came in: one collection's request count, or the
// totals across several collections and environments.
func importTitle(result imports.Result, requests int) string {
	var parts []string
	if requests > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", requests, plural(requests, "request", "requests")))
	}
	if len(result.Environments) > 0 {
		variables := 0
		for _, env := range result.Environments {
			variables += len(env.Values)
		}
		parts = append(parts, fmt.Sprintf("%d %s in %d %s", variables, plural(variables, "variable", "variables"), len(result.Environments), plural(len(result.Environments), "environment", "environments")))
	}
	if len(result.Collections) == 1 {
		return fmt.Sprintf("Imported “%s” · %s", result.Collections[0].Name, strings.Join(parts, " · "))
	}
	return "Imported " + strings.Join(parts, " · ")
}

// uniqueWarnings keeps the first of each warning, so the report does not
// repeat one for every environment or folder it applies to.
func uniqueWarnings(list []string) []string {
	var out []string
	for _, warning := range list {
		if warning == "" || slices.Contains(out, warning) {
			continue
		}
		out = append(out, warning)
	}
	return out
}

// mergeFolders lets the incoming settings of a path replace the saved ones,
// so importing the same collection again updates rather than duplicates.
func mergeFolders(saved, incoming []request.Folder) []request.Folder {
	if len(incoming) == 0 {
		return saved
	}
	out := slices.Clone(saved)
	for _, folder := range incoming {
		at := slices.IndexFunc(out, func(existing request.Folder) bool { return existing.Path == folder.Path })
		if at < 0 {
			out = append(out, folder)
			continue
		}
		out[at] = folder
	}
	return out
}

// mergeImportedEnvironments merges values into the environment of the same
// name, keeping its local current values and unrelated variables.
func (a *app) mergeImportedEnvironments(incoming []imports.Environment) (title string, warnings []string, err error) {
	if a.envsProblem != "" {
		return "", nil, fmt.Errorf("%s", a.envsProblem)
	}
	envs := slices.Clone(a.envs)
	var added, updated int
	for _, imported := range incoming {
		name := strings.TrimSpace(imported.Name)
		if name == "" {
			name = "Imported environment"
		}
		at := slices.IndexFunc(envs, func(existing environment) bool { return existing.Name == name })
		if at < 0 {
			envs = append(envs, environment{Name: name, Vars: maps.Clone(imported.Values)})
			added++
		} else {
			vars := maps.Clone(envs[at].Vars)
			if vars == nil {
				vars = map[string]string{}
			}
			maps.Copy(vars, imported.Values)
			envs[at].Vars = vars
			updated++
		}
		warnings = append(warnings, imported.Warnings...)
	}
	if err := saveEnvs(a.file(a.envsPath), envFile{Active: a.activeEnv, Envs: envs, Globals: a.globals}); err != nil {
		return "", warnings, err
	}
	a.envs = envs
	switch {
	case len(incoming) == 1:
		return fmt.Sprintf("Imported environment “%s” · %d %s", strings.TrimSpace(incoming[0].Name), len(incoming[0].Values), plural(len(incoming[0].Values), "variable", "variables")), warnings, nil
	case added > 0 && updated == 0:
		return fmt.Sprintf("Imported %d %s", added, plural(added, "environment", "environments")), warnings, nil
	default:
		return fmt.Sprintf("Merged %d %s", len(incoming), plural(len(incoming), "environment", "environments")), warnings, nil
	}
}
