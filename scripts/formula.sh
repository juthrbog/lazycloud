#!/usr/bin/env bash
# Writes the Homebrew formula for a release to standard output.
#
# Usage: scripts/formula.sh VERSION CHECKSUMS
#
# VERSION is the version without "v", for example 0.6.0. CHECKSUMS is the
# checksums.txt file of the release.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 VERSION CHECKSUMS" >&2
  exit 2
fi
version="$1"
sums="$2"
base="https://github.com/juthrbog/lazycloud/releases/download/v${version}"

# sha prints the checksum of the archive for a platform, such as linux_amd64.
sha() {
  local file="lazycloud_${version}_$1.tar.gz" sum
  sum="$(awk -v f="$file" '$2 == f { print $1 }' "$sums")"
  if [[ ! "$sum" =~ ^[0-9a-f]{64}$ ]]; then
    echo "no checksum for $file in $sums" >&2
    exit 1
  fi
  echo "$sum"
}

darwin_arm64="$(sha darwin_arm64)"
darwin_amd64="$(sha darwin_amd64)"
linux_arm64="$(sha linux_arm64)"
linux_amd64="$(sha linux_amd64)"

cat <<FORMULA
# Generated from the release's SHA-256 checksums. Do not edit manually.
class Lazycloud < Formula
  desc "Terminal UI for browsing and managing AWS resources"
  homepage "https://github.com/juthrbog/lazycloud"
  version "${version}"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "${base}/lazycloud_${version}_darwin_arm64.tar.gz"
      sha256 "${darwin_arm64}"
    end
    on_intel do
      url "${base}/lazycloud_${version}_darwin_amd64.tar.gz"
      sha256 "${darwin_amd64}"
    end
  end

  on_linux do
    on_arm do
      url "${base}/lazycloud_${version}_linux_arm64.tar.gz"
      sha256 "${linux_arm64}"
    end
    on_intel do
      url "${base}/lazycloud_${version}_linux_amd64.tar.gz"
      sha256 "${linux_amd64}"
    end
  end

  def install
    bin.install "lazycloud"
  end

  def caveats
    <<~EOS
      LazyCloud starts in ReadOnly mode. Press W to allow changes.
      SSM Connect needs the AWS CLI and the Session Manager plugin.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/lazycloud --version")
  end
end
FORMULA
