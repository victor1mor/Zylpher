#!/usr/bin/env bash
set -euo pipefail

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
version=v9.8.7
if grep -Fxq "versao=$version" README.md; then version=v9.8.8; fi
script=.github/scripts/release-readme.sh
mkdir "$work/repo"
git init --bare -q -b main "$work/origin.git"
cp README.md "$work/repo/"
cp "$script" "$work/repo/release-readme.sh"
cd "$work/repo"
git init -q -b main
git config user.name "Release test"
git config user.email "release@example.invalid"
git add README.md release-readme.sh
git commit -qm "Fixture"
git remote add origin "$work/origin.git"
git push -qu origin main
base=$(git rev-parse HEAD)
git config push.followTags true
git tag -a local-only -m 'Tag fora da release'

if bash release-readme.sh "$version" --check; then
    echo "O README com versão antiga deveria ser rejeitado." >&2
    exit 1
fi
bash release-readme.sh "$version" --tag
release=$(git rev-parse HEAD)
[[ $(git rev-parse "$version^{commit}") == "$release" ]]
[[ $(git --git-dir="$work/origin.git" rev-parse "refs/tags/$version^{commit}") == "$release" ]]
[[ $(git --git-dir="$work/origin.git" rev-parse refs/heads/main) == "$release" ]]
[[ -z $(git --git-dir="$work/origin.git" tag --list local-only) ]]
[[ $(git diff --name-only "$base" "$release") == README.md ]]
git show "$version:README.md" > tagged-readme
cmp README.md tagged-readme
rm tagged-readme

grep -Fxq "[release $version](https://github.com/victor1mor/Zylpher/releases/tag/$version)" README.md
grep -Fxq "versao=$version" README.md
grep -Fxq "\$versao = '$version'" README.md
grep -Fxq "go get github.com/victor1mor/Zylpher/client@$version" README.md
grep -Fq "Para instalar a $version pública" README.md
[[ $(grep -Fxc "git clone --branch $version --depth 1 https://github.com/victor1mor/Zylpher.git" README.md) -eq 2 ]]
bash release-readme.sh "$version" --check
bash release-readme.sh "$version" --tag
[[ $(git rev-parse HEAD) == "$release" ]]
