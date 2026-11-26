#!/usr/bin/env bash
set -euo pipefail
umask 022
export LC_ALL=C TZ=UTC GOWORK=off CGO_ENABLED=0 GOAMD64=v1 GOARM64=v8.0

version=$1
if [[ ! $version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    echo "Versão inválida, use vMAJOR.MINOR.PATCH." >&2
    exit 1
fi
mkdir -p -- "$2"
output=$(realpath -- "$2")
[[ -z $(ls -A -- "$output") && -z $(git status --porcelain) ]]
commit=$(git rev-parse HEAD)
stamp=$(git log -1 --format=%ct)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

repo="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-victor1mor/Zylpher}"
stem="zylpher-$version"
source="$work/$stem-source"
mkdir "$source"
git archive HEAD | tar -xf - -C "$source"
go mod vendor -o "$source/vendor"
goroot=$(go env GOROOT)
mkdir "$source/toolchain-licenses"
cp "$goroot/LICENSE" "$goroot/PATENTS" "$source/toolchain-licenses/"

gpl="$source/vendor/go.mau.fi/libsignal/LICENSE"
[[ -f $gpl ]]
grep -q 'GNU GENERAL PUBLIC LICENSE' "$gpl"
cat > "$work/LEIA-ME.md" <<EOF
# Zylpher $version

Commit $commit. O pacote inclui zylpher, zylpher-probe e
zylpher-webhook-receiver. Para configurar e executar, consulte o README:

$repo/blob/$version/README.md

## Licenças e código-fonte

Os executáveis zylpher e zylpher-probe incorporam go.mau.fi/libsignal
e são distribuídos como obras combinadas sob a GNU GPL, versão 3.
O código original do Zylpher está sob a licença Apache 2.0.
O receptor separado segue as licenças das dependências que incorpora.

Código-fonte correspondente, incluindo as dependências e avisos de licença:

$repo/releases/download/$version/$stem-source.tar.gz
EOF
cat "$work/LEIA-ME.md" > "$source/README-FONTE.md"
cat >> "$source/README-FONTE.md" <<'EOF'

## Compilar o código-fonte correspondente

Use a toolchain do go.mod. O diretório vendor contém as dependências
necessárias para compilar sem baixar módulos. Em Linux ou macOS:

    mkdir -p bin
    CGO_ENABLED=0 go build -mod=vendor -trimpath -buildvcs=false -ldflags="-s -w" -o bin/ ./cmd/...

Em PowerShell:

    New-Item -ItemType Directory -Force bin
    $env:CGO_ENABLED = '0'
    go build -mod=vendor -trimpath -buildvcs=false '-ldflags=-s -w' -o bin/ ./cmd/...

Defina GOOS e GOARCH para compilar para outra plataforma.
EOF

(cd "$source" && GOPROXY=off go list -mod=vendor -deps ./cmd/... >/dev/null)

archive_tar() {
    # ponytail: GNU tar e gzip no runner mantêm ordem e timestamps estáveis
    # para reaproveitar os anexos de um rascunho em uma nova tentativa.
    tar -C "$work" --sort=name --mtime="@$stamp" --owner=0 --group=0 \
        --numeric-owner -cf - "$1" | gzip -n > "$output/$1.tar.gz"
    tar -tzf "$output/$1.tar.gz" >/dev/null
}

archive_tar "$stem-source"

programs=(zylpher zylpher-probe zylpher-webhook-receiver)
for goos in linux windows darwin; do
    os=$goos
    extension=
    if [[ $goos == darwin ]]; then os=macos; fi
    if [[ $goos == windows ]]; then extension=.exe; fi

    for arch in amd64 arm64; do
        name="$stem-$os-$arch"
        dir="$work/$name"
        echo "Compilando $goos/$arch"
        mkdir "$dir" "$dir/third-party-licenses"
        GOOS="$goos" GOARCH="$arch" go build -mod=readonly -trimpath \
            -buildvcs=true -ldflags='-s -w' -o "$dir/" \
            ./cmd/zylpher ./cmd/zylpher-probe ./cmd/zylpher-webhook-receiver

        info=
        for program in "${programs[@]}"; do
            binary=$(go version -m "$dir/$program$extension")
            for setting in "GOOS=$goos" "GOARCH=$arch" "CGO_ENABLED=0" \
                "vcs.revision=$commit" 'vcs.modified=false'; do
                grep -Fxq $'\tbuild\t'"$setting" <<< "$binary"
            done
            info+="$binary"$'\n'
        done
        if [[ $goos/$arch == linux/amd64 ]]; then
            "$dir/zylpher-probe" -h >/dev/null 2>&1
        fi

        cp LICENSE "$work/LEIA-ME.md" "$dir/"
        cp "$gpl" "$dir/LICENSE-GPL-3.0.txt"
        cp -R "$source/toolchain-licenses" "$dir/third-party-licenses/Go"
        while IFS= read -r module; do
            module_path="$source/vendor/$module"
            [[ -d $module_path ]]
            found=false
            while IFS= read -r -d '' notice; do
                install -D -m 0644 "$notice" "$dir/third-party-licenses/${notice#"$source/vendor/"}"
                found=true
            done < <(find "$module_path" -type f \( -iname 'license*' -o -iname 'licence*' \
                -o -iname 'notice*' -o -iname 'copying*' -o -iname 'copyright*' -o -iname 'patents*' \) -print0)
            [[ $found == true ]] || { echo "Aviso de licença ausente: $module" >&2; exit 1; }
        done < <(awk '$1 == "dep" {print $2}' <<< "$info" | sort -u)
        if [[ $goos == linux ]]; then cp -R deploy "$dir/"; fi

        if [[ $goos == windows ]]; then
            (
                cd "$work"
                find "$name" -type f -exec touch -d "@$stamp" {} +
                find "$name" -type f | sort | zip -X -q "$output/$name.zip" -@
            )
            unzip -tq "$output/$name.zip"
        else
            archive_tar "$name"
        fi
    done
done

(
    cd "$output"
    sha256sum ./*.tar.gz ./*.zip | sed 's#  \./#  #' > SHA256SUMS
    sha256sum --check --strict SHA256SUMS
)
[[ -z $(git status --porcelain) ]]
