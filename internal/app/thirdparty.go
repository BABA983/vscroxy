package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/imports"
	"github.com/baba983/vscroxy/internal/request"
)

// importedFolderLocal is a folder's local Current values and credential
// marks from an import, kept out of the library write.
type importedFolderLocal struct {
	path        string
	current     map[string]string
	credentials map[string]bool
}

// importThirdParty merges one third-party export into the library. The
// reader has already converted it to requests, folders and environments;
// this owns the app's single library write and its report.
func (a *app) importThirdParty(result imports.Result, what string) {
	var incoming []savedRequest
	var incomingFolders []request.Folder
	var incomingLocal []importedFolderLocal
	var warnings []string
	warnings = append(warnings, result.Warnings...)
	for _, collection := range result.Collections {
		for _, r := range collection.Requests {
			incoming = append(incoming, savedRequest{Request: r})
		}
		incomingFolders = append(incomingFolders, collection.Folders...)
		for _, folder := range collection.Folders {
			local := importedFolderLocal{path: folder.Path, current: map[string]string{}, credentials: map[string]bool{}}
			for _, v := range folder.Variables {
				if v.Current != "" || v.Local {
					local.current[v.Name] = v.Current
					if v.Credential {
						local.credentials[v.Name] = true
					}
				}
			}
			if len(local.current) > 0 {
				incomingLocal = append(incomingLocal, local)
			}
		}
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
		replaced := replacedFolderPaths(a.requests, a.folders, incomingFolders, result.Collections)
		merged := mergeRequests(a.requests, incoming)
		folders := mergeFolders(a.folders, incomingFolders)
		if err := a.writeLibrary(merged, folders); err != nil {
			a.fail(what, err)
			return
		}
		a.applyImportedFolderCurrent(replaced, incomingLocal)
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

// replacedFolderPaths names the imported collection roots and folder paths
// that already existed, so their old local Current values do not attach to
// the replacement. A collection root may have no saved folder settings, so
// requests also tell whether it existed.
func replacedFolderPaths(savedRequests []savedRequest, savedFolders, incoming []request.Folder, collections []imports.Collection) []string {
	var out []string
	for _, collection := range collections {
		path := cleanPath(collection.Name)
		exists := slices.ContainsFunc(savedRequests, func(r savedRequest) bool { return within(r.Collection, path) }) ||
			slices.ContainsFunc(savedFolders, func(existing request.Folder) bool { return within(existing.Path, path) })
		if exists && !slices.Contains(out, path) {
			out = append(out, path)
		}
	}
	for _, folder := range incoming {
		if slices.ContainsFunc(savedFolders, func(existing request.Folder) bool { return existing.Path == folder.Path }) && !slices.Contains(out, folder.Path) {
			out = append(out, folder.Path)
		}
	}
	return out
}

// applyImportedFolderCurrent drops the old collection's local values when an
// import replaces it, then keeps the new import's own local values. A replaced
// collection root also drops its old descendants; a replaced folder drops its
// own entry.
func (a *app) applyImportedFolderCurrent(replaced []string, incoming []importedFolderLocal) {
	if len(replaced) == 0 && len(incoming) == 0 {
		return
	}
	next := maps.Clone(a.folderCurrent)
	if next == nil {
		next = map[string]map[string]string{}
	}
	credentials := maps.Clone(a.folderCredentials)
	if credentials == nil {
		credentials = map[string]map[string]bool{}
	}
	for _, path := range replaced {
		if strings.Contains(path, "/") {
			delete(next, path)
			delete(credentials, path)
			continue
		}
		for p := range next {
			if within(p, path) {
				delete(next, p)
			}
		}
		for p := range credentials {
			if within(p, path) {
				delete(credentials, p)
			}
		}
	}
	for _, local := range incoming {
		if len(local.current) == 0 {
			continue
		}
		next[local.path] = local.current
		if len(local.credentials) > 0 {
			credentials[local.path] = local.credentials
		} else {
			delete(credentials, local.path)
		}
	}
	if err := a.saveFolderCurrent(next, credentials); err != nil {
		a.warn("Imported collection saved, but local Current values were not updated: " + err.Error())
	}
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
			envs = append(envs, environment{Name: name, Vars: maps.Clone(imported.Values), Current: maps.Clone(imported.Current), Credentials: maps.Clone(imported.Credentials)})
			added++
		} else {
			vars := maps.Clone(envs[at].Vars)
			if vars == nil {
				vars = map[string]string{}
			}
			maps.Copy(vars, imported.Values)
			current := maps.Clone(envs[at].Current)
			if current == nil && len(imported.Current) > 0 {
				current = map[string]string{}
			}
			maps.Copy(current, imported.Current)
			credentials := maps.Clone(envs[at].Credentials)
			if credentials == nil && len(imported.Credentials) > 0 {
				credentials = map[string]bool{}
			}
			maps.Copy(credentials, imported.Credentials)
			envs[at].Vars, envs[at].Current, envs[at].Credentials = vars, current, credentials
			updated++
		}
		warnings = append(warnings, imported.Warnings...)
	}
	f := a.localEnvs()
	f.Envs = envs
	if err := saveEnvs(a.file(a.envsPath), f); err != nil {
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
