#!/usr/bin/env bash
# install-claude-skills.sh — install this repo's agent-facing Claude assets so a
# Claude session running inside an argus sandbox discovers argus's MCP tools.
#
# Two opt-in steps, each prompted (Y/n):
#   1. Symlink every skill under .claude/skills/* into ~/.claude/skills/<name>.
#   2. Append every snippet under claude/snippets/*.md into ~/.claude/CLAUDE.md
#      between per-snippet managed markers.
#
# Both steps are idempotent and safe to re-run. Pairs with uninstall-claude-skills.sh.
#
# Usage:
#   ./install-claude-skills.sh        # interactive, prompts before each step
#   ./install-claude-skills.sh --yes  # non-interactive, accept both steps

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

# Managed-marker strings for a snippet named <name>.
snippet_begin() { printf '# >>> claude-snippet:%s >>>' "$1"; }
snippet_end()   { printf '# <<< claude-snippet:%s <<<' "$1"; }

# --- 1. skills ---------------------------------------------------------------

install_skills() {
  bold "1/2  Skills → ${CLAUDE_SKILLS_DIR}"

  if [[ ! -d "${SKILLS_SRC_DIR}" ]] || [[ -z "$(ls -A "${SKILLS_SRC_DIR}" 2>/dev/null)" ]]; then
    warn "  no skills found under ${SKILLS_SRC_DIR}; nothing to install"
    echo
    return 0
  fi

  if ! confirm "  Symlink this repo's skills into ${CLAUDE_SKILLS_DIR}?"; then
    warn "  skipped skill install."
    echo
    return 0
  fi

  mkdir -p "${CLAUDE_SKILLS_DIR}"
  local src name link current
  for src in "${SKILLS_SRC_DIR}"/*/; do
    src="${src%/}"
    name="$(basename "${src}")"
    link="${CLAUDE_SKILLS_DIR}/${name}"
    if [[ -L "${link}" ]]; then
      current="$(readlink "${link}")"
      if [[ "${current}" == "${src}" ]]; then
        green "  ✓ ${name} is already current"
      else
        warn "  ${link} is a symlink to ${current}; leaving it alone (remove it and re-run to relink)"
      fi
    elif [[ -e "${link}" ]]; then
      warn "  ${link} already exists and is not a symlink; leaving it alone"
    else
      ln -s "${src}" "${link}"
      green "  ✓ symlinked ${name} → ${src}"
    fi
  done
  echo
}

# --- 2. snippets -------------------------------------------------------------

append_snippet() {
  # append_snippet <name> <src-file> — write/replace the named managed block.
  local name="$1" src="$2" begin end tmp
  begin="$(snippet_begin "${name}")"
  end="$(snippet_end "${name}")"

  tmp="$(mktemp)"
  if [[ -f "${CLAUDE_MD}" ]]; then
    awk -v b="${begin}" -v e="${end}" '
      $0 == b {drop=1}
      drop && $0 == e {drop=0; next}
      !drop {print}
    ' "${CLAUDE_MD}" > "${tmp}"
  fi
  {
    if [[ -s "${tmp}" ]]; then echo; fi
    echo "${begin}"
    cat "${src}"
    echo "${end}"
  } >> "${tmp}"
  mv "${tmp}" "${CLAUDE_MD}"
  green "  ✓ wrote snippet block '${name}' into ${CLAUDE_MD}"
}

install_snippets() {
  bold "2/2  Snippets → ${CLAUDE_MD}"

  if [[ ! -d "${SNIPPETS_SRC_DIR}" ]] || ! compgen -G "${SNIPPETS_SRC_DIR}/*.md" >/dev/null; then
    warn "  no snippets found under ${SNIPPETS_SRC_DIR}; nothing to install"
    echo
    return 0
  fi

  if ! confirm "  Append this repo's orientation snippet(s) to ${CLAUDE_MD}?"; then
    warn "  skipped snippet wiring. To add them yourself, wire in:"
    local src
    for src in "${SNIPPETS_SRC_DIR}"/*.md; do warn "        ${src}"; done
    echo
    return 0
  fi

  if [[ -L "${CLAUDE_MD}" ]]; then
    warn "  note: ${CLAUDE_MD} is a symlink (likely compiled output)."
    warn "        An append here may be overwritten on your next recompile —"
    warn "        consider dropping the snippet(s) into your snippets pipeline instead."
  fi

  mkdir -p "$(dirname "${CLAUDE_MD}")"
  local src name
  for src in "${SNIPPETS_SRC_DIR}"/*.md; do
    name="$(basename "${src}" .md)"
    append_snippet "${name}" "${src}"
  done
  echo
}

# --- run ---------------------------------------------------------------------

bold "install-claude-skills"
echo
install_skills
install_snippets
bold "Done."
echo "Inside an argus worktree, fresh Claude sessions will reach for the installed skills"
echo "automatically. Remove everything later with ./uninstall-claude-skills.sh."
