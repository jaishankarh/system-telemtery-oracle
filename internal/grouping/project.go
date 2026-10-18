package grouping

import (
	"path"
	"strings"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// ProjectRoot is the Project a working directory belongs to: its nearest
// ancestor holding a VCS marker (.git, .hg, .svn, .jj, .bzr), otherwise the
// working directory itself.
func ProjectRoot(m machine.Machine, cwd string) string {
	cwd = strings.TrimSuffix(cwd, " (deleted)")
	for dir := path.Clean(cwd); ; dir = path.Dir(dir) {
		for _, mk := range vcsMarkers {
			if m.Exists(path.Join(dir, mk)) {
				return dir
			}
		}
		if dir == "/" || dir == "." {
			return cwd
		}
	}
}
