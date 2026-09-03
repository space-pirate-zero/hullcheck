// Package scan orchestrates a reading: discover rules, discover gates, match.
package scan

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/space-pirate-zero/hullcheck/internal/gates"
	"github.com/space-pirate-zero/hullcheck/internal/match"
	"github.com/space-pirate-zero/hullcheck/internal/model"
	"github.com/space-pirate-zero/hullcheck/internal/rules"
)

// ErrNoPolicy is returned when a repository states no rules at all. It is an
// explicit refusal, not a 100% score: a reading with no denominator is a lie.
var ErrNoPolicy = errors.New("no policy documents found: nothing to check the hull against")

// Run reads the repository at root.
func Run(root string) (model.Report, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return model.Report{}, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return model.Report{}, errors.New("not a directory: " + root)
	}

	rs, docs, err := rules.Discover(abs)
	if err != nil {
		return model.Report{}, err
	}
	if len(docs) == 0 {
		return model.Report{}, ErrNoPolicy
	}
	gs, err := gates.Discover(abs)
	if err != nil {
		return model.Report{}, err
	}
	gs = gates.Promote(gs)

	rep := match.Run(rs, gs)
	rep.Root = root
	rep.Docs = docs
	return rep, nil
}
