#!/usr/bin/env bash
# uninstall-claude-skills.sh — remove the agent-facing Claude assets that
# install-claude-skills.sh installed. Idempotent and safe to re-run.
#
# Two opt-in steps, each prompted (Y/n):
#   1. Remove ~/.claude/skills/<name> for every skill under .claude/skills/* —
#      but ONLY when it is a symlink pointing back at THIS repo (never touches a
#      real directory or a foreign symlink).
#   2. Strip every per-snippet managed block (claude/snippets/*.md) from
#      ~/.claude/CLAUDE.md, leaving the rest of the file untouched.
#
# Usage:
#   ./uninstall-claude-skills.sh        # interactive, prompts before each step
#   ./uninstall-claude-skills.sh --yes  # non-interactive, accept both steps

set -euo pipefail

# --- config ------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILLS_SRC_DIR="${SCRIPT_DIR}/.claude/skills"
SNIPPETS_SRC_DIR="${SCRIPT_DIR}/claude/snippets"
CLAUDE_SKILLS_DIR="${HOME}/.claude/skills"
CLAUDE_MD="${HOME}/.claude/CLAUDE.md"

NON_INTERACTIVE=false
for arg in "$@"; do
  case "$arg" in
    --yes|-y) NON_INTERACTIVE=true ;;
    *) echo "unknown flag: $arg" >&2; exit 1 ;;
  esac
done

# --- helpers ----------------------------------------------------------------

bold()  { printf '\033[1m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
warn()  { printf '\033[33m%s\033[0m\n' "$*"; }

confirm() {
  if $NON_INTERACTIVE; then
    return 0
  fi
  read -r -p "$1 [Y/n] " reply
  [[ -z "$reply" || "$reply" =~ ^[Yy] ]]
}

snippet_begin() { printf '# >>> claude-snippet:%s >>>' "$1"; }
snippet_end()   { printf '# <<< claude-snippet:%s <<<' "$1"; }

# --- 1. skills ---------------------------------------------------------------

uninstall_skills() {
  bold "1/2  Skills"

  if [[ ! -d "${SKILLS_SRC_DIR}" ]] || [[ -z "$(ls -A "${SKILLS_SRC_DIR}" 2>/dev/null)" ]]; then
    warn "  no skills defined under ${SKILLS_SRC_DIR}; nothing to remove"
    echo
    return 0
  fi

  if ! confirm "  Remove this repo's skill symlinks from ${CLAUDE_SKILLS_DIR}?"; then
    warn "  skipped skill removal."
    echo
    return 0
  fi

  local src name link current removed=false
  for src in "${SKILLS_SRC_DIR}"/*/; do
    src="${src%/}"
    name="$(basename "${src}")"
    link="${CLAUDE_SKILLS_DIR}/${name}"
    if [[ -L "${link}" ]]; then
      current="$(readlink "${link}")"
      if [[ "${current}" == "${src}" ]]; then
        rm "${link}"
        green "  ✓ removed symlink ${link}"
        removed=true
      else
        warn "  ${link} points at ${current} (not this repo); leaving it alone"
      fi
    elif [[ -e "${link}" ]]; then
      warn "  ${link} is not a symlink; leaving it alone"
    fi
  done
  $removed || green "  ✓ nothing to remove"
  echo
}

# --- 2. snippets -------------------------------------------------------------

strip_snippet() {
  # strip_snippet <name> — remove the named managed block from CLAUDE.md.
  # Returns 0 if a block was removed, 1 otherwise.
  local name="$1" begin end tmp before after
  begin="$(snippet_begin "${name}")"
  end="$(snippet_end "${name}")"

  [[ -f "${CLAUDE_MD}" ]] || return 1
  grep -qF "${begin}" "${CLAUDE_MD}" || return 1

  before="$(wc -l < "${CLAUDE_MD}")"
  tmp="$(mktemp)"
  awk -v b="${begin}" -v e="${end}" '
    $0 == b {drop=1}
    drop && $0 == e {drop=0; next}
    !drop {print}
  ' "${CLAUDE_MD}" > "${tmp}"
  mv "${tmp}" "${CLAUDE_MD}"
  after="$(wc -l < "${CLAUDE_MD}")"
  [[ "${before}" -ne "${after}" ]]
}

uninstall_snippets() {
  bold "2/2  Snippets"

  if [[ ! -f "${CLAUDE_MD}" ]]; then
    warn "  ${CLAUDE_MD} does not exist; nothing to remove"
    echo
    return 0
  fi

  if [[ ! -d "${SNIPPETS_SRC_DIR}" ]] || ! compgen -G "${SNIPPETS_SRC_DIR}/*.md" >/dev/null; then
    warn "  no snippets defined under ${SNIPPETS_SRC_DIR}; nothing to remove"
    echo
    return 0
  fi

  if ! confirm "  Strip this repo's snippet block(s) from ${CLAUDE_MD}?"; then
    warn "  skipped snippet removal."
    echo
    return 0
  fi

  if [[ -L "${CLAUDE_MD}" ]]; then
    warn "  note: ${CLAUDE_MD} is a symlink (likely compiled output)."
    warn "        Edits here may be overwritten on your next recompile."
  fi

  local src name removed=false
  for src in "${SNIPPETS_SRC_DIR}"/*.md; do
    name="$(basename "${src}" .md)"
    if strip_snippet "${name}"; then
      green "  ✓ removed snippet block '${name}' from ${CLAUDE_MD}"
      removed=true
    fi
  done
  $removed || green "  ✓ nothing to remove"
  echo
}

# --- run ---------------------------------------------------------------------

bold "uninstall-claude-skills"
echo
uninstall_skills
uninstall_snippets
bold "Done."
