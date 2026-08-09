#!/usr/bin/env bash

set -euo pipefail

event_name="${GITHUB_EVENT_NAME:-}"
event_path="${GITHUB_EVENT_PATH:-}"

if [[ -z "$event_name" || ! -f "$event_path" ]]; then
  echo "GitHub event metadata is required" >&2
  exit 1
fi

conventional_subject='^(feat|fix|docs|test|refactor|perf|chore|ci|build|revert)(\([a-z0-9][a-z0-9._/-]*\))?!?: .+'

validate_subject() {
  local kind="$1"
  local subject="$2"

  if ! printf '%s\n' "$subject" | grep -Eq "$conventional_subject"; then
    echo "Invalid $kind: $subject" >&2
    echo "Expected Conventional Commit syntax, for example: feat(ui): add status chrome" >&2
    return 1
  fi
}

commit_range=""

case "$event_name" in
  pull_request)
    pull_request_title="$(jq -r '.pull_request.title // ""' "$event_path")"
    pull_request_body="$(jq -r '.pull_request.body // ""' "$event_path")"
    validate_subject "pull request title" "$pull_request_title"

    release_label_count=0
    release_label=""
    while IFS= read -r label; do
      case "$label" in
        breaking-change|enhancement|bug|dependencies|github_actions|documentation|skip-changelog)
          release_label_count=$((release_label_count + 1))
          release_label="$label"
          ;;
      esac
    done < <(jq -r '.pull_request.labels[]?.name' "$event_path" | sort -u)

    if [[ "$release_label_count" -ne 1 ]]; then
      echo "A pull request must have exactly one release disposition label" >&2
      echo "Expected one of: breaking-change, enhancement, bug, dependencies, github_actions, documentation, skip-changelog" >&2
      exit 1
    fi

    has_breaking_marker=false
    if [[ "$pull_request_title" =~ !: ]] || [[ "$pull_request_body" == *"BREAKING CHANGE:"* ]]; then
      has_breaking_marker=true
    fi

    if [[ "$has_breaking_marker" == true && "$release_label" != "breaking-change" ]]; then
      echo "Breaking changes require the breaking-change label" >&2
      exit 1
    fi

    if [[ "$release_label" == "breaking-change" && "$has_breaking_marker" != true ]]; then
      echo "The breaking-change label requires ! or BREAKING CHANGE: in the PR" >&2
      exit 1
    fi

    base_sha="$(jq -r '.pull_request.base.sha' "$event_path")"
    head_sha="$(jq -r '.pull_request.head.sha' "$event_path")"
    commit_range="${base_sha}..${head_sha}"
    ;;
  push)
    before_sha="$(jq -r '.before // ""' "$event_path")"
    after_sha="$(jq -r '.after // ""' "$event_path")"
    if [[ -z "$after_sha" ]]; then
      echo "The push event does not contain an after SHA" >&2
      exit 1
    fi
    if [[ "$before_sha" =~ ^0+$ ]]; then
      commit_range="$after_sha"
    else
      commit_range="${before_sha}..${after_sha}"
    fi
    ;;
  *)
    echo "Skipping release metadata checks for event: $event_name"
    exit 0
    ;;
esac

while IFS= read -r commit; do
  [[ -z "$commit" ]] && continue
  commit_sha="${commit%%$'\t'*}"
  commit_subject="${commit#*$'\t'}"
  validate_subject "commit $commit_sha" "$commit_subject"
done < <(git log --no-merges --format='%H%x09%s' "$commit_range")

echo "Release metadata checks passed"
