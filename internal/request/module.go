package request

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"sync"

	"github.com/evanw/esbuild/pkg/api"
)

// module is an ES module (or a CommonJS file) as goja runs it: esbuild turns
// import and export into require and module.exports, and lowers syntax newer
// than ES2017. sourceMap is the comment that maps errors back to the file as
// written; it must be the last line of what goja compiles.
type module struct {
	code, sourceMap string
	exports         []string
}

// compileModule converts src, named file in errors and stacks. A file that
// does not parse, or awaits at its top level, is a ScriptProblem.
func compileModule(file, src string) (module, error) {
	stdin := &api.StdinOptions{Contents: src, Sourcefile: file, Loader: api.LoaderJS}
	// The ES module build is only for what the file exports, which esbuild
	// reports for that format alone.
	esm := api.Build(api.BuildOptions{Stdin: stdin, Format: api.FormatESModule, Target: api.ES2017,
		Metafile: true, Write: false, LogLevel: api.LogLevelSilent, Outfile: "out.js"})
	if err := buildProblem(esm.Errors); err != nil {
		return module{}, err
	}
	var meta struct {
		Outputs map[string]struct {
			Exports []string `json:"exports"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(esm.Metafile), &meta); err != nil {
		return module{}, err
	}
	cjs := api.Build(api.BuildOptions{Stdin: stdin, Format: api.FormatCommonJS, Target: api.ES2017,
		Sourcemap: api.SourceMapInline, Write: false, LogLevel: api.LogLevelSilent, Outfile: "out.js"})
	if err := buildProblem(cjs.Errors); err != nil {
		return module{}, err
	}
	code := strings.TrimRight(string(cjs.OutputFiles[0].Contents), "\n")
	m := module{code: code, exports: meta.Outputs["out.js"].Exports}
	if at := strings.LastIndex(code, "\n//# sourceMappingURL="); at >= 0 {
		m.code, m.sourceMap = code[:at], code[at+1:]
	}
	return m, nil
}

func buildProblem(errs []api.Message) error {
	if len(errs) == 0 {
		return nil
	}
	e := errs[0]
	if e.Location == nil {
		return fmt.Errorf("%s", e.Text)
	}
	return ScriptProblem{Line: e.Location.Line, Column: e.Location.Column + 1, Message: e.Text}
}

// addonModules keeps addons converted, since every script run requires them
// again; an edit is a new key.
var addonModules = struct {
	sync.Mutex
	m map[string]module
}{m: map[string]module{}}

func addonModule(name, src string) (module, error) {
	key := name + "\x00" + src
	addonModules.Lock()
	m, ok := addonModules.m[key]
	addonModules.Unlock()
	if ok {
		return m, nil
	}
	m, err := compileModule("addons/"+name+".js", src)
	if err != nil {
		return m, err
	}
	addonModules.Lock()
	if len(addonModules.m) >= 256 {
		clear(addonModules.m)
	}
	addonModules.m[key] = m
	addonModules.Unlock()
	return m, nil
}

// CheckModule says where an addon does not parse, as the editor shows it.
func CheckModule(name, src string) error {
	_, err := addonModule(name, src)
	return err
}
