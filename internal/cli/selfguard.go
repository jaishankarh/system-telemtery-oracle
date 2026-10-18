package cli

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jaishankarh/system-telemtery-oracle/internal/selfguard"
)

// installCmd runs install or uninstall.
func installCmd(env Env, cmd string) int {
	var err error
	if cmd == "install" {
		err = selfguard.Install(env.Machine)
	} else {
		err = selfguard.Uninstall(env.Machine)
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "%s: %v\n", cmd, err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "%s: done\n", cmd)
	return 0
}

// withSelf serves /api/self in front of the main handler.
func withSelf(g *selfguard.Guard, next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/self", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(g.Status())
	})
	mux.Handle("/", next)
	return mux
}
