package main

import (
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/infrahq/infra/internal/tools/querylinter"
)

func main() {
	singlechecker.Main(querylinter.Analyzer)
}

// New implements the current golangci-lint Go plugin entrypoint.
func New(_ any) ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{querylinter.Analyzer}, nil
}
