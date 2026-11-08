// This file only prevents `go test ./...` / `go vet ./...` from walking into
// node_modules. The frontend itself is an Angular project, not a Go module.
module github.com/ananta888/dockweave/ui/web

go 1.27.3
