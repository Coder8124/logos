package brain

import (
	"io"

	"github.com/Coder8124/brain/internal/mcpserver"
)

// ServeMCP speaks the Model Context Protocol over the given streams, exposing
// this vault's tools to any MCP host. Use it to put your own product in front
// of brain's memory rather than reimplementing the tool surface.
//
// A missing model runtime is not an error, matching Open: retrieval degrades to
// lexical and every continuity tool works untouched. Refusing to serve would
// have contradicted the rest of this API, which is built to keep working on a
// machine with no models on it.
//
// It blocks until the input stream closes.
func (b *Brain) ServeMCP(in io.Reader, out io.Writer) error {
	return mcpserver.New(b.ix.DB, b.rt, b.ix.Vault).Serve(in, out)
}
