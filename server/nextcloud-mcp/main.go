// Command nextcloud-mcp is a stdio MCP server for Nextcloud.
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/mark3labs/mcp-go/server"

	"nextcloud-mcp/confirm"
	"nextcloud-mcp/nc"
	"nextcloud-mcp/tools"
)

func main() {
	os.Exit(run(os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}

// run serves MCP over in/out until in reaches EOF. stdout carries only MCP
// traffic; logs go to errw.
func run(env func(string) string, in io.Reader, out, errw io.Writer) int {
	var missing []string
	for _, k := range []string{"NEXTCLOUD_URL", "NEXTCLOUD_USER", "NEXTCLOUD_PASSWORD"} {
		if env(k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(errw, "nextcloud-mcp: faltan variables de entorno: %s\n", strings.Join(missing, ", "))
		return 1
	}

	loc, err := time.LoadLocation(env("TZ"))
	if err != nil || env("TZ") == "" {
		loc, err = time.LoadLocation("America/Santiago")
		if err != nil {
			fmt.Fprintf(errw, "nextcloud-mcp: zona horaria: %v\n", err)
			return 1
		}
	}

	d := &tools.Deps{
		NC:      nc.New(env("NEXTCLOUD_URL"), env("NEXTCLOUD_USER"), env("NEXTCLOUD_PASSWORD")),
		Confirm: confirm.New(time.Now, rand.Reader),
		Now:     time.Now,
		Loc:     loc,
	}
	s := server.NewMCPServer("nextcloud", "1.0.0", server.WithToolCapabilities(false))
	for _, t := range tools.All(d) {
		s.AddTool(t.Def, t.Handle)
	}

	ss := server.NewStdioServer(s)
	ss.SetErrorLogger(log.New(errw, "nextcloud-mcp ", log.LstdFlags))
	if err := ss.Listen(context.Background(), in, out); err != nil && err != io.EOF {
		fmt.Fprintf(errw, "nextcloud-mcp: %v\n", err)
		return 1
	}
	return 0
}
