{ lib
, buildGoModule
, git
, version
,
}:

buildGoModule {
  pname = "vesnik";
  inherit version;

  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./go.mod
      ./go.sum
      ./cmd
      ./internal
    ];
  };

  vendorHash = "sha256-0sSVCQnLA272kx211bSw7FZqZw6e+4KIpSPQPpqXD8k=";


  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  # The tests drive real git repositories.
  nativeCheckInputs = [ git ];

  meta = {
    description = "AI-written release notes and version bumps, reviewed before anything ships";
    homepage = "https://github.com/nklmilojevic/vesnik";
    license = lib.licenses.mit;
    mainProgram = "vesnik";
  };
}
