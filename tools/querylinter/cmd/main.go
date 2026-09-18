package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/infrahq/infra/tools/querylinter"
)

func main() {
	singlechecker.Main(querylinter.Analyzer)
}
