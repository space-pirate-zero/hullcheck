# pushed.sh - sourced by pre-push. It moves the hook into a scratch worktree of
# the commit being pushed, so the gates judge exactly what lands and not whatever
# happens to be checked out: `git push origin other-branch`, or a push with edits
# still uncommitted, would otherwise be checked against the wrong tree.
#
# git writes one "<local ref> <local sha> <remote ref> <remote sha>" line per ref
# on stdin. Each distinct commit gets its own run of the hook; a deletion pushes
# nothing and is not checked.
if [ -z "${HULLCHECK_PUSHED:-}" ]; then
	zero=0000000000000000000000000000000000000000
	status=0
	seen=" "
	while read -r _ sha _ _; do
		[ "$sha" = "$zero" ] && continue
		case "$seen" in *" $sha "*) continue ;; esac
		seen="$seen$sha "
		echo "pre-push: checking $(git rev-parse --short "$sha")"
		HULLCHECK_PUSHED=$sha "$0" "$@" </dev/null || status=1
	done
	exit "$status"
fi

repo=$(pwd)
tree=$(mktemp -d)
git worktree add -q --detach "$tree" "$HULLCHECK_PUSHED"
trap 'cd "$repo" && git worktree remove --force "$tree"' EXIT
cd "$tree"
# Only this hook's re-run may see it. Left exported, every gate inherits it, and
# a test that runs a hook of its own would take itself for a re-run.
unset HULLCHECK_PUSHED
