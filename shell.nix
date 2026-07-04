with import <nixpkgs> { };
pkgs.mkShell {
  name = "dmp-project";

  NIX_LD_LIBRARY_PATH = lib.makeLibraryPath [
    stdenv.cc.cc # libstdc++
    zlib # libz (for numpy)
  ];

  NIX_LD = lib.fileContents "${stdenv.cc}/nix-support/dynamic-linker";

  SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
  GIT_SSL_CAINFO = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
  REQUESTS_CA_BUNDLE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";

  packages = with pkgs; [
    uv
    basedpyright
    ruff
    black
    nodejs_24
    cmake
    gcc
    gnumake
    go # api-gateway + identity-service (Phase 1, see docs/architecture/implementation-plan.md)
  ];

  shellHook = ''
    # Fix for npm/npx trying to access read-only nix store paths
    mkdir -p "$HOME/.npm-global/bin" "$HOME/.npm-global/lib"
    export npm_config_prefix="$HOME/.npm-global"
    export PATH="$HOME/.npm-global/bin:$PATH"

    # Keep the library path for Python and Next.js SWC binaries
    export LD_LIBRARY_PATH=$NIX_LD_LIBRARY_PATH
  '';
}
