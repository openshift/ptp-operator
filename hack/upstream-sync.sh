#!/usr/bin/env bash
#
# upstream-sync.sh - Sync upstream PRs into the downstream repo
#
# Finds merged PRs from the upstream repo that aren't yet in the downstream
# repo, extracts bug references (OCPBUGS-*, CNF-*) from PR titles, bodies,
# commit messages, trailers, and linked Jira issues, then creates (or updates)
# a downstream PR with those references in the title.
#
# When all PRs can be taken as-is, the sync branch points directly at
# upstream HEAD (fast path). When some PRs are skipped, the script
# cherry-picks only the selected merge commits onto the downstream branch.
# Merge conflicts are detected and reported in the PR body without
# attempting automatic resolution.
#
# PR skip detection (evaluated in order):
#   1. Manual skip list    - SKIP_PRS / SKIP_COMMITS env vars
#   2. Automatic detection - PRs whose changed files do not exist in the
#                            downstream branch are skipped (zero config).
#   3. Ignore patterns     - If .upstream-sync-ignore exists, PRs that
#                            exclusively modify matching paths are skipped.
#
# In CI, this script is run by the upstream-sync GitHub Actions workflow
# which provides GH_TOKEN automatically via GITHUB_TOKEN.
#
# For local use / testing:
#   1. Create a GitHub Personal Access Token (PAT):
#      - Go to https://github.com/settings/tokens
#      - Click "Generate new token (classic)"
#      - Select the "repo" scope (full control of private repositories)
#      - For public repos, "public_repo" scope is sufficient
#      - Copy the generated token
#   2. Export it:
#      export GH_TOKEN="ghp_your_token_here"
#   3. Run the script:
#      ./hack/upstream-sync.sh
#
# Flags:
#   --dry-run        Run all read-only steps (fetch, analyze, log) but skip
#                    pushing branches and creating/updating PRs.
#   --keep-worktree  Do not remove the temporary git worktree after pushing.
#                    Useful for inspecting or fixing conflicts locally.
#
# Environment variables:
#   UPSTREAM_REMOTE      Git remote name for the upstream repo (default: upstream)
#   UPSTREAM_BRANCH      Branch to sync from upstream (default: main)
#   DOWNSTREAM_REMOTE    Git remote name for the downstream repo (default: origin)
#   DOWNSTREAM_BRANCH    Branch to target downstream (default: main)
#   SYNC_BRANCH_PREFIX   Prefix for the sync branch name (default: upstream-sync-)
#   FORK_REMOTE          Git remote name for a personal fork; when set, pushes
#                        go to the fork and cross-repo PRs are created.
#   WORKTREE_ROOT        Parent directory for temporary worktrees (default: /tmp)
#   BUG_PATTERN          Regex for bug references (default: (OCPBUGS|CNF)-[0-9]+)
#   REVIEWERS            Space-separated list of GitHub usernames to cc on PRs
#   MERGE_BASE_OVERRIDE  Commit SHA to use as merge base (testing/dry-run)
#   SKIP_PRS             Comma-separated PR numbers to skip (e.g. "42,99")
#   SKIP_COMMITS         Comma-separated commit SHAs to skip (prefix match)
#   SYNC_IGNORE_FILE     Path to the ignore-pattern file (default: .upstream-sync-ignore)
#   JIRA_BASE_URL        Jira instance URL (default: https://redhat.atlassian.net;
#                        set empty to disable Jira scanning)
#   JIRA_PROJECTS        Comma-separated Jira project keys (default: OCPBUGS)
#   JIRA_COMPONENTS      Comma-separated Jira component names to filter on
#
# .upstream-sync-ignore file format:
#   One pattern per line. Lines starting with # are comments.
#   Patterns ending with / match directory prefixes.
#   Other patterns are matched literally against the full file path.
#   Example:
#     # Skip CI-only upstream changes
#     .github/
#     Makefile.upstream
#
set -euo pipefail

# --- Parse flags ---
DRY_RUN=false
KEEP_WORKTREE=false
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    --keep-worktree) KEEP_WORKTREE=true ;;
    *) echo "Unknown argument: $arg" >&2; exit 1 ;;
  esac
done

# --- Configuration (env var overrides with defaults) ---
UPSTREAM_REMOTE="${UPSTREAM_REMOTE:-upstream}"
UPSTREAM_BRANCH="${UPSTREAM_BRANCH:-main}"
DOWNSTREAM_REMOTE="${DOWNSTREAM_REMOTE:-origin}"
DOWNSTREAM_BRANCH="${DOWNSTREAM_BRANCH:-main}"
SYNC_BRANCH_PREFIX="${SYNC_BRANCH_PREFIX:-upstream-sync-}"
FORK_REMOTE="${FORK_REMOTE:-}"
WORKTREE_ROOT="${WORKTREE_ROOT:-/tmp}"
BUG_PATTERN="${BUG_PATTERN:-(OCPBUGS|CNF)-[0-9]+}"
REVIEWERS="${REVIEWERS:-}"
SKIP_PRS="${SKIP_PRS:-}"
SKIP_COMMITS="${SKIP_COMMITS:-}"
SYNC_IGNORE_FILE="${SYNC_IGNORE_FILE:-.upstream-sync-ignore}"

# Jira configuration (set JIRA_BASE_URL="" to disable Jira scanning)
JIRA_BASE_URL="${JIRA_BASE_URL:-https://redhat.atlassian.net}"
JIRA_PROJECTS="${JIRA_PROJECTS:-OCPBUGS}"
JIRA_COMPONENTS="${JIRA_COMPONENTS:-Networking / ptp,Cloud Native Events / Cloud Event Proxy}"

# Derive owner/repo from git remote URLs if not explicitly set
if [ -z "${UPSTREAM_REPO:-}" ]; then
  if git remote get-url "$UPSTREAM_REMOTE" &>/dev/null; then
    UPSTREAM_REPO=$(git remote get-url "$UPSTREAM_REMOTE" | sed -E 's#.*(github\.com[:/])##; s/\.git$//')
  else
    UPSTREAM_REPO="k8snetworkplumbingwg/ptp-operator"
  fi
fi
if [ -z "${DOWNSTREAM_REPO:-}" ]; then
  DOWNSTREAM_REPO=$(git remote get-url "$DOWNSTREAM_REMOTE" | sed -E 's#.*(github\.com[:/])##; s/\.git$//')
fi

# When FORK_REMOTE is set, push to the fork and create cross-repo PRs
PUSH_REMOTE="$DOWNSTREAM_REMOTE"
FORK_REPO=""
if [ -n "$FORK_REMOTE" ]; then
  PUSH_REMOTE="$FORK_REMOTE"
  FORK_REPO=$(git remote get-url "$FORK_REMOTE" | sed -E 's#.*(github\.com[:/])##; s/\.git$//')
  FORK_OWNER="${FORK_REPO%%/*}"
fi

# --- State (populated by functions) ---
EXISTING_PR_NUMBER=""
EXISTING_BRANCH=""
MERGE_BASE=""
UPSTREAM_HEAD=""
FILTERED_PRS=""
BUG_LIST=""
SYNC_COMMITS=""
SKIPPED_PRS="[]"
HAS_SKIPS=false
CONFLICT_FILES=""

# --- Helpers ---

log() {
  local prefix=""
  if [ "$DRY_RUN" = true ]; then
    prefix="[DRY RUN] "
  fi
  echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] ${prefix}$*"
}

# --- Functions ---

fetch_remotes() {
  if ! git remote get-url "$UPSTREAM_REMOTE" &>/dev/null; then
    log "Adding remote ${UPSTREAM_REMOTE}: https://github.com/${UPSTREAM_REPO}.git"
    git remote add "$UPSTREAM_REMOTE" "https://github.com/${UPSTREAM_REPO}.git"
  fi
  log "Fetching all remotes..."
  git fetch --all
}

get_sync_range() {
  if [ -n "${MERGE_BASE_OVERRIDE:-}" ]; then
    MERGE_BASE="$MERGE_BASE_OVERRIDE"
    log "Using overridden merge base: ${MERGE_BASE}"
  else
    MERGE_BASE=$(git merge-base "${DOWNSTREAM_REMOTE}/${DOWNSTREAM_BRANCH}" "${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}")
  fi
  UPSTREAM_HEAD=$(git rev-parse "${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}")

  log "Merge base: ${MERGE_BASE}"
  log "Upstream HEAD: ${UPSTREAM_HEAD}"

  if [ "$MERGE_BASE" = "$UPSTREAM_HEAD" ]; then
    log "Already up to date, nothing to sync."
    exit 0
  fi

  local commit_count
  commit_count=$(git rev-list --count "${MERGE_BASE}..${UPSTREAM_HEAD}")
  log "${commit_count} new commits to sync"
}

collect_upstream_prs() {
  local merge_base_date
  merge_base_date=$(git log -1 --format=%aI "$MERGE_BASE")
  log "Searching for upstream PRs merged after ${merge_base_date}..."

  local upstream_prs_json
  upstream_prs_json=$(gh pr list \
    --repo "$UPSTREAM_REPO" \
    --state merged \
    --search "merged:>=${merge_base_date}" \
    --json number,title,body,mergeCommit \
    --limit 200)

  local range_shas
  range_shas=$(git log --format=%H "${MERGE_BASE}..${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}")

  FILTERED_PRS=$(echo "$upstream_prs_json" | jq --arg shas "$range_shas" '
    ($shas | split("\n")) as $valid |
    [ .[] | select(.mergeCommit.oid as $sha | $valid | index($sha)) ]
  ')

  local pr_count
  pr_count=$(echo "$FILTERED_PRS" | jq 'length')
  log "Found ${pr_count} merged upstream PRs in range:"

  echo "$FILTERED_PRS" | jq -r '.[] | "  #\(.number) - \(.title)"' | while IFS= read -r line; do
    log "$line"
  done
}

should_skip_pr() {
  local pr_number="$1"
  local changed_files
  changed_files=$(gh api "repos/${UPSTREAM_REPO}/pulls/${pr_number}/files" \
    --jq '.[].filename' 2>/dev/null) || return 1

  if [ -z "$changed_files" ]; then
    return 1
  fi

  local any_exists_downstream=false
  while IFS= read -r file; do
    [ -z "$file" ] && continue
    if git cat-file -e "${DOWNSTREAM_REMOTE}/${DOWNSTREAM_BRANCH}:${file}" 2>/dev/null; then
      any_exists_downstream=true
      break
    fi
  done <<< "$changed_files"

  if [ "$any_exists_downstream" = false ]; then
    echo "all changed files are upstream-only (not present downstream)"
    return 0
  fi

  local ignore_file="$SYNC_IGNORE_FILE"
  if [ -f "$ignore_file" ]; then
    local all_ignored=true
    while IFS= read -r file; do
      [ -z "$file" ] && continue
      local matched=false
      while IFS= read -r pattern; do
        [ -z "$pattern" ] && continue
        [[ "$pattern" == \#* ]] && continue
        if [[ "$pattern" == */ ]]; then
          [[ "$file" == ${pattern}* ]] && matched=true
        else
          [[ "$file" == $pattern ]] && matched=true
        fi
      done < "$ignore_file"
      if [ "$matched" = false ]; then
        all_ignored=false
        break
      fi
    done <<< "$changed_files"
    if [ "$all_ignored" = true ]; then
      echo "all changed files match ignore patterns"
      return 0
    fi
  fi

  return 1
}

filter_skipped() {
  log "Filtering PRs for skip conditions..."

  local skip_pr_list skip_commit_list
  IFS=',' read -ra skip_pr_list <<< "${SKIP_PRS:-}"
  IFS=',' read -ra skip_commit_list <<< "${SKIP_COMMITS:-}"

  local kept_prs="[]"
  local skipped_prs="[]"
  local pr_count
  pr_count=$(echo "$FILTERED_PRS" | jq 'length')

  for (( i=0; i<pr_count; i++ )); do
    local pr_number pr_title pr_sha
    pr_number=$(echo "$FILTERED_PRS" | jq -r ".[$i].number")
    pr_title=$(echo "$FILTERED_PRS" | jq -r ".[$i].title")
    pr_sha=$(echo "$FILTERED_PRS" | jq -r ".[$i].mergeCommit.oid")

    local skip_reason=""

    for skip_num in "${skip_pr_list[@]}"; do
      skip_num="${skip_num// /}"
      if [ -n "$skip_num" ] && [ "$skip_num" = "$pr_number" ]; then
        skip_reason="manually skipped (SKIP_PRS)"
        break
      fi
    done

    if [ -z "$skip_reason" ]; then
      for skip_sha in "${skip_commit_list[@]}"; do
        skip_sha="${skip_sha// /}"
        if [ -n "$skip_sha" ] && [[ "$pr_sha" == ${skip_sha}* ]]; then
          skip_reason="manually skipped (SKIP_COMMITS)"
          break
        fi
      done
    fi

    if [ -z "$skip_reason" ]; then
      if skip_reason=$(should_skip_pr "$pr_number" 2>/dev/null); then
        : # PR should be skipped, reason is in skip_reason
      else
        skip_reason=""
      fi
    fi

    if [ -n "$skip_reason" ]; then
      log "  SKIP #${pr_number} - ${pr_title} (${skip_reason})"
      skipped_prs=$(echo "$skipped_prs" | jq \
        --arg n "$pr_number" --arg t "$pr_title" --arg r "$skip_reason" \
        '. + [{"number":($n|tonumber),"title":$t,"reason":$r}]')
      HAS_SKIPS=true
    else
      kept_prs=$(echo "$kept_prs" | jq --argjson pr "$(echo "$FILTERED_PRS" | jq ".[$i]")" '. + [$pr]')
    fi
  done

  SKIPPED_PRS="$skipped_prs"
  FILTERED_PRS="$kept_prs"

  if [ "$HAS_SKIPS" = true ]; then
    SYNC_COMMITS=$(echo "$FILTERED_PRS" | jq -r '.[].mergeCommit.oid')
    local kept_count skipped_count
    kept_count=$(echo "$FILTERED_PRS" | jq 'length')
    skipped_count=$(echo "$SKIPPED_PRS" | jq 'length')
    log "Keeping ${kept_count} PRs, skipping ${skipped_count}"
  else
    log "No PRs skipped"
  fi
}

scan_bugs() {
  local bugs_from_titles bugs_from_bodies bugs_from_commits bugs_from_trailers

  log "Scanning for bug references matching: ${BUG_PATTERN}"

  bugs_from_titles=$(echo "$FILTERED_PRS" | jq -r '.[].title' | grep -oE "$BUG_PATTERN" || true)
  bugs_from_bodies=$(echo "$FILTERED_PRS" | jq -r '.[].body // ""' | grep -oE "$BUG_PATTERN" || true)
  bugs_from_commits=$(git log --format=%B "${MERGE_BASE}..${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}" | grep -oE "$BUG_PATTERN" || true)

  bugs_from_trailers=$(git log --format='%(trailers:key=Closes,valueonly)%(trailers:key=Fixes,valueonly)%(trailers:key=Resolves,valueonly)%(trailers:key=Fix,valueonly)%(trailers:key=Close,valueonly)%(trailers:key=Resolve,valueonly)' \
    "${MERGE_BASE}..${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}" | grep -oE "$BUG_PATTERN" || true)

  BUG_LIST=$(printf '%s\n' "$bugs_from_titles" "$bugs_from_bodies" "$bugs_from_commits" "$bugs_from_trailers" \
    | grep -E "^${BUG_PATTERN}$" | sort -u | sed ':a;N;$!ba;s/\n/, /g' || true)

  if [ -n "$BUG_LIST" ]; then
    log "Bugs found:"
    echo "$BUG_LIST" | tr ',' '\n' | while IFS= read -r bug; do
      log "  $bug"
    done
  else
    log "No bug references found"
  fi
}

scan_bugs_from_jira() {
  if [ -z "$JIRA_BASE_URL" ]; then
    log "Jira scanning disabled (JIRA_BASE_URL is empty)"
    return 0
  fi

  local jira_api="${JIRA_BASE_URL}/rest/api/3"

  local merge_base_date
  merge_base_date=$(git log -1 --format=%aI "$MERGE_BASE" | cut -dT -f1)

  local known_pr_numbers
  known_pr_numbers=$(echo "$FILTERED_PRS" | jq -r '.[].number' 2>/dev/null | sort -u)
  if [ -z "$known_pr_numbers" ]; then
    known_pr_numbers=$(git log --format=%s "${MERGE_BASE}..${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}" \
      | grep -oP '(?<=Merge pull request #)\d+' | sort -u || true)
  fi

  if [ -z "$known_pr_numbers" ]; then
    log "No PR numbers to cross-reference with Jira, skipping"
    return 0
  fi
  log "PR numbers for Jira cross-reference: $(echo "$known_pr_numbers" | tr '\n' ' ')"

  local upstream_repo_pattern="${UPSTREAM_REPO}/pull/"
  local jira_bugs=""

  for project in $(echo "$JIRA_PROJECTS" | tr ',' ' '); do
    local jql="project = ${project}"
    if [ -n "$JIRA_COMPONENTS" ]; then
      local comp_list
      comp_list=$(echo "$JIRA_COMPONENTS" | awk -F',' '{for(i=1;i<=NF;i++){gsub(/^ +| +$/,"",$i); printf "\"%s\"", $i; if(i<NF) printf ", "}}')
      jql+=" AND component in (${comp_list})"
    fi
    jql+=" AND updatedDate >= \"${merge_base_date}\""

    local encoded_jql
    encoded_jql=$(echo -n "$jql" | jq -sRr @uri)

    local search_url="${jira_api}/search/jql?jql=${encoded_jql}&fields=key&maxResults=200"

    log "Querying Jira: ${jql}"

    local issues_json
    issues_json=$(curl -sL --max-time 30 "$search_url" 2>/dev/null) || { log "WARNING: Jira search failed, skipping"; continue; }

    local issue_keys
    issue_keys=$(echo "$issues_json" | jq -r '.issues[].key' 2>/dev/null) || { log "WARNING: Failed to parse Jira response"; continue; }

    local issue_count
    issue_count=$(echo "$issue_keys" | grep -c . || echo 0)
    log "Found ${issue_count} Jira issues to check remote links"

    while IFS= read -r key; do
      [ -z "$key" ] && continue
      local links_json
      links_json=$(curl -sL --max-time 10 "${jira_api}/issue/${key}/remotelink" 2>/dev/null) || continue

      local pr_urls
      pr_urls=$(echo "$links_json" | jq -r ".[].object.url // empty" 2>/dev/null | grep "$upstream_repo_pattern" || true)

      for url in $pr_urls; do
        local pr_num
        pr_num=$(echo "$url" | grep -oP '(?<=/pull/)\d+')
        if echo "$known_pr_numbers" | grep -qx "$pr_num"; then
          log "  ${key} linked to upstream PR #${pr_num}"
          jira_bugs+="${key}"$'\n'
        fi
      done
    done <<< "$issue_keys"
  done

  if [ -z "$jira_bugs" ]; then
    log "No additional bugs found from Jira"
    return 0
  fi

  local unique_jira_bugs
  unique_jira_bugs=$(echo "$jira_bugs" | grep -E "^${BUG_PATTERN}$" | sort -u || true)

  if [ -z "$unique_jira_bugs" ]; then
    return 0
  fi

  log "Bugs found from Jira:"
  echo "$unique_jira_bugs" | while IFS= read -r bug; do
    log "  $bug"
  done

  local existing_bugs=""
  if [ -n "$BUG_LIST" ]; then
    existing_bugs=$(echo "$BUG_LIST" | tr ',' '\n' | sed 's/^ //')
  fi
  BUG_LIST=$(printf '%s\n%s' "$existing_bugs" "$unique_jira_bugs" \
    | grep -E "^${BUG_PATTERN}$" | sort -u | sed ':a;N;$!ba;s/\n/, /g' || true)
}

check_existing_sync_pr() {
  log "Checking for existing open sync PR (branch prefix: ${SYNC_BRANCH_PREFIX})..."

  local existing_pr
  existing_pr=$(gh pr list --repo "$DOWNSTREAM_REPO" --state open \
    --json number,headRefName \
    --jq "[.[] | select(.headRefName | startswith(\"${SYNC_BRANCH_PREFIX}\"))] | first // empty")

  if [ -n "$existing_pr" ]; then
    EXISTING_PR_NUMBER=$(echo "$existing_pr" | jq -r '.number')
    EXISTING_BRANCH=$(echo "$existing_pr" | jq -r '.headRefName')
    log "Existing sync PR #${EXISTING_PR_NUMBER} found on branch ${EXISTING_BRANCH}"
  else
    log "No existing sync PR found, will create a new one"
  fi
}

cleanup_worktree() {
  local dir="$1" branch="$2"
  if [ -d "$dir" ]; then
    git worktree remove --force "$dir" 2>/dev/null || true
  fi
  git branch -D "$branch" 2>/dev/null || true
}

detect_conflicts() {
  local worktree_dir="$1"
  local original_dir
  original_dir=$(pwd)
  cd "$worktree_dir"

  log "Testing for merge conflicts against ${DOWNSTREAM_REMOTE}/${DOWNSTREAM_BRANCH}..."
  if git merge --no-commit --no-ff "${DOWNSTREAM_REMOTE}/${DOWNSTREAM_BRANCH}" 2>/dev/null; then
    git merge --abort 2>/dev/null || true
    cd "$original_dir"
    log "No merge conflicts detected"
    return 0
  fi

  CONFLICT_FILES=$(git diff --name-only --diff-filter=U 2>/dev/null || true)
  git merge --abort 2>/dev/null || true
  cd "$original_dir"

  if [ -n "$CONFLICT_FILES" ]; then
    log "WARNING: Merge conflicts detected in the following files:"
    echo "$CONFLICT_FILES" | while IFS= read -r f; do
      log "  - $f"
    done
  fi
}

push_sync_branch() {
  local branch_name
  local worktree_dir
  local force_flag=""

  if [ -n "$EXISTING_PR_NUMBER" ]; then
    branch_name="$EXISTING_BRANCH"
    force_flag="--force"
  else
    branch_name="${SYNC_BRANCH_PREFIX}$(date +%Y-%m-%d)"
    EXISTING_BRANCH="$branch_name"
  fi

  worktree_dir="${WORKTREE_ROOT}/${branch_name}"

  if [ "$HAS_SKIPS" = true ]; then
    log "Skipped PRs detected: cherry-picking selected commits onto ${DOWNSTREAM_REMOTE}/${DOWNSTREAM_BRANCH}..."
  else
    log "Pushing ${branch_name} to ${PUSH_REMOTE} (upstream HEAD: ${UPSTREAM_HEAD})..."
  fi

  if [ "$DRY_RUN" = true ]; then
    if [ "$HAS_SKIPS" = true ]; then
      log "Would cherry-pick $(echo "$SYNC_COMMITS" | wc -w | tr -d ' ') commits onto ${DOWNSTREAM_REMOTE}/${DOWNSTREAM_BRANCH}"
    else
      log "Would create worktree at ${worktree_dir}, push ${branch_name} to ${PUSH_REMOTE}"
    fi
    if [ "$KEEP_WORKTREE" = true ]; then
      log "Worktree would be kept at ${worktree_dir}"
    fi
    return 0
  fi

  cleanup_worktree "$worktree_dir" "$branch_name"

  if [ "$HAS_SKIPS" = true ]; then
    git branch "$branch_name" "${DOWNSTREAM_REMOTE}/${DOWNSTREAM_BRANCH}"
    git worktree add "$worktree_dir" "$branch_name"

    local original_dir
    original_dir=$(pwd)
    cd "$worktree_dir"

    local cherry_ok=true
    for sha in $SYNC_COMMITS; do
      local cp_flags=""
      local parent_count
      parent_count=$(git cat-file -p "$sha" | grep -c '^parent' || echo 1)
      if [ "$parent_count" -gt 1 ]; then
        cp_flags="-m 1"
      fi

      if ! git cherry-pick $cp_flags --no-commit "$sha" 2>/dev/null; then
        log "WARNING: Cherry-pick of ${sha} had conflicts"
        local conflicting
        conflicting=$(git diff --name-only --diff-filter=U 2>/dev/null || true)
        if [ -n "$conflicting" ]; then
          CONFLICT_FILES=$(printf '%s\n%s' "$CONFLICT_FILES" "$conflicting" | sort -u | sed '/^$/d')
          echo "$conflicting" | while IFS= read -r f; do
            log "  conflict: $f"
          done
        fi
        git cherry-pick --abort 2>/dev/null || git reset --hard HEAD
        cherry_ok=false
        continue
      fi
      git commit --no-edit -m "cherry-pick upstream $(git log -1 --format=%s "$sha")" 2>/dev/null || true
    done

    cd "$original_dir"
  else
    git branch "$branch_name" "$UPSTREAM_HEAD"
    git worktree add "$worktree_dir" "$branch_name"
    detect_conflicts "$worktree_dir"
  fi

  local original_dir
  original_dir=$(pwd)
  cd "$worktree_dir"

  if ! git push $force_flag "$PUSH_REMOTE" "$branch_name"; then
    cd "$original_dir"
    if [ "$KEEP_WORKTREE" = false ]; then
      cleanup_worktree "$worktree_dir" "$branch_name"
    fi
    if [ -z "$FORK_REMOTE" ]; then
      log "ERROR: Push to ${PUSH_REMOTE} failed. If you don't have push access, set FORK_REMOTE to push via a fork instead."
      log "  Example: FORK_REMOTE=my_fork ./hack/upstream-sync.sh"
    else
      log "ERROR: Push to fork ${PUSH_REMOTE} failed."
    fi
    return 1
  fi

  cd "$original_dir"

  if [ "$KEEP_WORKTREE" = true ]; then
    log "Worktree kept at ${worktree_dir}"
  else
    cleanup_worktree "$worktree_dir" "$branch_name"
  fi

  log "Pushed ${branch_name} to ${PUSH_REMOTE}"
}

build_pr_title() {
  local today
  today=$(date '+%d-%b-%Y')

  if [ -n "$BUG_LIST" ]; then
    echo "${BUG_LIST}: Sync from upstream (${today})"
  else
    echo "Sync from upstream (${today})"
  fi
}

build_pr_body() {
  local body=""
  body+="## Upstream PRs included"$'\n\n'

  local pr_count
  pr_count=$(echo "$FILTERED_PRS" | jq 'length')

  for (( i=0; i<pr_count; i++ )); do
    local pr_number pr_title pr_bugs
    pr_number=$(echo "$FILTERED_PRS" | jq -r ".[$i].number")
    pr_title=$(echo "$FILTERED_PRS" | jq -r ".[$i].title")
    pr_bugs=$(echo "$FILTERED_PRS" | jq -r ".[$i] | (.title // \"\") + \" \" + (.body // \"\")" \
      | grep -oE "$BUG_PATTERN" | sort -u | paste -sd ',' - || true)

    local line="- [#${pr_number}](https://github.com/${UPSTREAM_REPO}/pull/${pr_number}) ${pr_title}"
    if [ -n "$pr_bugs" ]; then
      line+=" (${pr_bugs})"
    fi
    body+="${line}"$'\n'
  done

  local skipped_count
  skipped_count=$(echo "$SKIPPED_PRS" | jq 'length')
  if [ "$skipped_count" -gt 0 ]; then
    body+=$'\n'"## Skipped PRs"$'\n\n'
    for (( i=0; i<skipped_count; i++ )); do
      local skip_number skip_title skip_reason
      skip_number=$(echo "$SKIPPED_PRS" | jq -r ".[$i].number")
      skip_title=$(echo "$SKIPPED_PRS" | jq -r ".[$i].title")
      skip_reason=$(echo "$SKIPPED_PRS" | jq -r ".[$i].reason")
      body+="- ~~[#${skip_number}](https://github.com/${UPSTREAM_REPO}/pull/${skip_number}) ${skip_title}~~ — ${skip_reason}"$'\n'
    done
  fi

  if [ -n "$CONFLICT_FILES" ]; then
    body+=$'\n'"## Merge conflicts"$'\n\n'
    body+="The following files have merge conflicts that need manual resolution:"$'\n\n'
    while IFS= read -r f; do
      [ -z "$f" ] && continue
      body+="- \`${f}\`"$'\n'
    done <<< "$CONFLICT_FILES"
    if [ -n "$FORK_REMOTE" ]; then
      body+=$'\n'"To resolve, clone the fork and fix conflicts locally:"$'\n'
      body+="\`\`\`bash"$'\n'
      body+="git clone https://github.com/${FORK_REPO}.git"$'\n'
      body+="cd ${FORK_REPO##*/}"$'\n'
      body+="git checkout ${EXISTING_BRANCH}"$'\n'
      body+="git merge ${DOWNSTREAM_REMOTE}/${DOWNSTREAM_BRANCH}"$'\n'
      body+="# resolve conflicts, then:"$'\n'
      body+="git push"$'\n'
      body+="\`\`\`"$'\n'
    fi
  fi

  if [ -n "$REVIEWERS" ]; then
    body+=$'\n'"cc"
    for user in $REVIEWERS; do
      body+=" @${user}"
    done
    body+=$'\n'
  fi

  echo "$body"
}

create_or_update_pr() {
  local pr_title pr_body
  pr_title=$(build_pr_title)
  pr_body=$(build_pr_body)

  log "PR title: ${pr_title}"
  log "PR body:"
  echo "$pr_body"

  local head_ref="$EXISTING_BRANCH"
  if [ -n "$FORK_REMOTE" ]; then
    head_ref="${FORK_OWNER}:${EXISTING_BRANCH}"
  fi

  if [ -n "$EXISTING_PR_NUMBER" ]; then
    log "Updating PR #${EXISTING_PR_NUMBER} title and body..."
    if [ "$DRY_RUN" = false ]; then
      gh pr edit "$EXISTING_PR_NUMBER" \
        --repo "$DOWNSTREAM_REPO" \
        --title "$pr_title" \
        --body "$pr_body"
    fi
    log "Updated PR #${EXISTING_PR_NUMBER}"
  else
    log "Creating new sync PR (head: ${head_ref} -> base: ${DOWNSTREAM_BRANCH})..."
    if [ "$DRY_RUN" = false ]; then
      local pr_url
      pr_url=$(gh pr create \
        --repo "$DOWNSTREAM_REPO" \
        --base "$DOWNSTREAM_BRANCH" \
        --head "$head_ref" \
        --title "$pr_title" \
        --body "$pr_body")
      log "Created PR: ${pr_url}"
    fi
  fi
}

# --- Main ---

main() {
  if [ -n "$FORK_REMOTE" ]; then
    log "Starting upstream sync: ${UPSTREAM_REPO} (${UPSTREAM_BRANCH}) -> ${DOWNSTREAM_REPO} (${DOWNSTREAM_BRANCH}) via fork ${FORK_REPO}"
  else
    log "Starting upstream sync: ${UPSTREAM_REPO} (${UPSTREAM_BRANCH}) -> ${DOWNSTREAM_REPO} (${DOWNSTREAM_BRANCH})"
  fi

  fetch_remotes
  get_sync_range
  collect_upstream_prs
  filter_skipped
  scan_bugs
  scan_bugs_from_jira
  check_existing_sync_pr
  push_sync_branch
  create_or_update_pr

  log "Upstream sync complete"
}

main "$@"
