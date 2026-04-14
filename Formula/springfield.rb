class Springfield < Formula
  desc "Plugin-first local CLI for Springfield setup and workflow control"
  homepage "https://github.com/OWNER/REPO"
  version "0.0.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/OWNER/REPO/releases/download/v0.0.0/springfield_0.0.0_darwin_arm64.tar.gz"
      sha256 "REPLACE_DARWIN_ARM64_SHA256"
    else
      url "https://github.com/OWNER/REPO/releases/download/v0.0.0/springfield_0.0.0_darwin_amd64.tar.gz"
      sha256 "REPLACE_DARWIN_AMD64_SHA256"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/OWNER/REPO/releases/download/v0.0.0/springfield_0.0.0_linux_arm64.tar.gz"
      sha256 "REPLACE_LINUX_ARM64_SHA256"
    else
      url "https://github.com/OWNER/REPO/releases/download/v0.0.0/springfield_0.0.0_linux_amd64.tar.gz"
      sha256 "REPLACE_LINUX_AMD64_SHA256"
    end
  end

  def install
    bin.install "springfield"
  end

  test do
    assert_match "springfield v0.0.0", shell_output("#{bin}/springfield version")
  end
end
