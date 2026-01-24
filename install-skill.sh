#!/bin/bash
#
# Install the speak skill to ~/.agents/skills/ and symlink to supported agents
#

set -e

SKILL_NAME="speak"
REPO_URL="https://github.com/blacktop/mcp-tts.git"
AGENTS_DIR="$HOME/.agents/skills"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

info() { echo -e "${BLUE}==>${NC} $1"; }
success() { echo -e "${GREEN}==>${NC} $1"; }
warn() { echo -e "${YELLOW}==>${NC} $1"; }
error() { echo -e "${RED}==>${NC} $1"; exit 1; }

# Check if running from cloned repo or standalone
if [[ -f "$SCRIPT_DIR/skill/SKILL.md" ]]; then
    SKILL_SOURCE="$SCRIPT_DIR/skill"
    info "Installing from local repo: $SCRIPT_DIR"
else
    info "Cloning $REPO_URL..."
    TMP_DIR=$(mktemp -d)
    trap "rm -rf $TMP_DIR" EXIT
    git clone --depth 1 "$REPO_URL" "$TMP_DIR" 2>/dev/null
    SKILL_SOURCE="$TMP_DIR/skill"
fi

# Create shared skills directory
info "Creating $AGENTS_DIR..."
mkdir -p "$AGENTS_DIR"

# Copy skill to shared location
if [[ -d "$AGENTS_DIR/$SKILL_NAME" ]]; then
    warn "Skill already exists at $AGENTS_DIR/$SKILL_NAME"
    read -p "Overwrite? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        error "Aborted"
    fi
    rm -rf "$AGENTS_DIR/$SKILL_NAME"
fi

info "Copying skill to $AGENTS_DIR/$SKILL_NAME..."
cp -r "$SKILL_SOURCE" "$AGENTS_DIR/$SKILL_NAME"
success "Skill installed to $AGENTS_DIR/$SKILL_NAME"

# Symlink to agents
declare -A AGENT_DIRS=(
    ["Claude Code"]="$HOME/.claude/skills"
    ["Codex CLI"]="$HOME/.codex/skills"
    ["Gemini CLI"]="$HOME/.gemini/skills"
)

echo
info "Creating symlinks to agents..."

for agent in "${!AGENT_DIRS[@]}"; do
    target_dir="${AGENT_DIRS[$agent]}"
    target_link="$target_dir/$SKILL_NAME"

    # Create agent skills directory if it doesn't exist
    mkdir -p "$target_dir"

    # Remove existing symlink or directory
    if [[ -L "$target_link" ]]; then
        rm "$target_link"
    elif [[ -d "$target_link" ]]; then
        warn "$agent: $target_link exists and is not a symlink, skipping"
        continue
    fi

    # Create symlink
    ln -sf "$AGENTS_DIR/$SKILL_NAME" "$target_link"
    success "$agent: $target_link -> $AGENTS_DIR/$SKILL_NAME"
done

echo
success "Done! The '$SKILL_NAME' skill is now available in:"
echo "  - Claude Code: /speak or auto-triggered"
echo "  - Codex CLI: restart to load"
echo "  - Gemini CLI: enable skills in /settings"
