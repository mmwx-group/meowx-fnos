#!/bin/bash
# 取 geo 数据随包带（第一次启动不用连 GitHub；内核的自动更新照常可开）：scripts/fetch-geo.sh <输出目录>
# 来源 MetaCubeX/meta-rules-dat 的 latest 发布，缓存在 .cache/geo（MEOWX_CACHE 可改；GEO_REFRESH=1 重新下载）。
set -euo pipefail
OUT="${1:?输出目录}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CACHE="${MEOWX_CACHE:-$ROOT/.cache}/geo"
BASE="https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest"
mkdir -p "$CACHE" "$OUT"
# 文件名照内核找的名字（内核按名字不分大小写找）
for pair in "geosite.dat:GeoSite.dat" "country.mmdb:Country.mmdb" "GeoLite2-ASN.mmdb:ASN.mmdb"; do
    src="${pair%%:*}"; dst="${pair##*:}"
    if [ ! -s "$CACHE/$dst" ] || [ -n "${GEO_REFRESH:-}" ]; then
        curl -fsSL --retry 3 -o "$CACHE/$dst.tmp" "$BASE/$src"
        mv "$CACHE/$dst.tmp" "$CACHE/$dst"
    fi
    cp "$CACHE/$dst" "$OUT/$dst"
done
ls -la "$OUT"
