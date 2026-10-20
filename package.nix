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

  vendorHash = "sha256-B5I1FFV3wDKOIgBIuoJrIgjDmUwv08W2m4TEeb/SITE=";


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
