#!/usr/bin/env bash
set -euo pipefail

version=$1
commit=$2
cd "$3"
sha256sum --check --strict SHA256SUMS
files=(SHA256SUMS ./*.tar.gz ./*.zip)

verify_tag() {
    local tagged
    tagged=$(gh api "repos/$GH_REPO/commits/$version" --jq .sha)
    [[ $tagged == "$commit" ]] || { echo "A tag diverge do commit empacotado." >&2; return 1; }
}

get_release() {
    gh api "repos/$GH_REPO/releases?per_page=100" --paginate --slurp |
        jq -c --arg tag "$version" '
            [.[][] | select(.tag_name == $tag)] |
            if length > 1 then error("Mais de uma release com esta tag") else .[0] end
        '
}

missing_assets() {
    local release=$1 file name hash asset id
    for file in "${files[@]}"; do
        name=${file##*/}
        hash=$(sha256sum -- "$file") || return
        asset=$(jq -c --arg name "$name" '.assets[] | select(.name == $name)' <<< "$release") || return
        if [[ -z $asset ]]; then
            printf '%s\n' "$file"
        elif jq -e --arg digest "sha256:${hash%% *}" \
            '.state == "uploaded" and .digest == $digest' <<< "$asset" >/dev/null; then
            continue
        elif [[ $draft == true ]] && jq -e '.state == "starter" and .size == 0' <<< "$asset" >/dev/null; then
            id=$(jq -r .id <<< "$asset") || return
            gh api --method DELETE "repos/$GH_REPO/releases/assets/$id" >/dev/null || return
            printf '%s\n' "$file"
        else
            echo "Anexo existente diverge: $name" >&2
            return 1
        fi
    done
}

verify_tag
marker="<!-- zylpher-release:$version:$commit -->"
release=$(get_release)
if [[ $release == null ]]; then
    notes=$(printf 'Consulte o [README da versão](%s/%s/blob/%s/README.md) para instalar.\n\nConfira os pacotes com SHA256SUMS. O pacote source inclui o código correspondente e os avisos de licença.\n\n%s\n' \
        "$GITHUB_SERVER_URL" "$GH_REPO" "$version" "$marker")
    gh release create "$version" --verify-tag --draft --title "Zylpher $version" --generate-notes --notes "$notes"
    release=$(get_release)
fi
draft=$(jq -r .draft <<< "$release")
if [[ $draft == true ]] && ! jq -e --arg marker "$marker" '(.body // "") | contains($marker)' <<< "$release" >/dev/null; then
    echo "Existe um rascunho manual para $version." >&2
    exit 1
fi

pending=$(missing_assets "$release")
if [[ $draft == false ]]; then
    [[ -z $pending ]] || { echo "A release publicada tem anexos faltando." >&2; exit 1; }
    jq -r .html_url <<< "$release"
    exit 0
fi
[[ $draft == true ]]
id=$(jq -r '.id' <<< "$release")
if [[ -n $pending ]]; then
    mapfile -t upload <<< "$pending"
    gh release upload "$version" "${upload[@]}"
fi

release=$(get_release)
jq -e --argjson id "$id" --arg marker "$marker" \
    '.id == $id and .draft and (.body | contains($marker))' <<< "$release" >/dev/null
pending=$(missing_assets "$release")
[[ -z $pending ]]
verify_tag
gh api --method PATCH "repos/$GH_REPO/releases/$id" -F draft=false --jq .html_url
