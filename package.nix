{ lib
, stdenv
, buildGoModule
, installShellFiles
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

  vendorHash = "sha256-84Kqck9kAnm4Q62abCy4upT5RCOexjvKoJBcPp3sMvM=";


  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  nativeBuildInputs = [ installShellFiles ];

  postInstall = lib.optionalString (stdenv.buildPlatform.canExecute stdenv.hostPlatform) ''
    installShellCompletion --cmd vesnik \
      --bash <($out/bin/vesnik completion bash) \
      --fish <($out/bin/vesnik completion fish) \
      --zsh <($out/bin/vesnik completion zsh)
  '';

  # The tests drive real git repositories.
  nativeCheckInputs = [ git ];

  meta = {
    description = "AI-written release notes and version bumps, reviewed before anything ships";
    homepage = "https://github.com/nklmilojevic/vesnik";
    license = lib.licenses.mit;
    mainProgram = "vesnik";
  };
}
