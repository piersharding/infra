package querylinter

import (
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

// init registers this analyzer as a golangci-lint v2 module plugin. See
// .custom-gcl.yml, which builds a golangci-lint binary with this plugin
// statically linked in (v2 dropped runtime-loaded Go-plugin .so files in
// favor of this, since .so loading required the plugin and the linter to be
// built with byte-identical dependency versions).
func init() {
	register.Plugin("querylinter", New)
}

type plugin struct{}

func New(_ any) (register.LinterPlugin, error) {
	return &plugin{}, nil
}

func (p *plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{Analyzer}, nil
}

func (p *plugin) GetLoadMode() string {
	return register.LoadModeSyntax
}
