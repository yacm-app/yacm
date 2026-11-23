# Scoop and Chocolatey

Both install the Windows zips of a GitHub release (x64 or ARM64); YACM keeps
its data in the user's folder, so an update keeps the sessions.

After a final release (not a release candidate):

```sh
node build/package-managers.mjs 1.2.0 SHA256SUMS.txt   # the release file
```

## Scoop

`scoop/yacm.json` is the manifest for the
[Extras bucket](https://github.com/ScoopInstaller/Extras). Once it is
there, the bucket's bot follows new releases by itself (`checkver`,
`autoupdate` with the hashes from `SHA256SUMS.txt`).

- First time: a pull request to ScoopInstaller/Extras adding
  `bucket/yacm.json` (their template asks for a linked issue "yacm@1.2.0:
  package request" and a check with `scoop install .\bucket\yacm.json`).
- Then: nothing; `scoop update yacm` gets new versions.

## Chocolatey

`chocolatey/` is the package for the
[community repository](https://community.chocolatey.org/packages/yacm). The
script downloads the zip for the computer's architecture, checks its
SHA-256, and adds YACM to the Start menu.

On Windows, with an API key of the yacm-app account on
community.chocolatey.org:

```powershell
cd build\chocolatey
choco pack
choco install yacm --source . -y     # try it
choco push yacm.<version>.nupkg --source https://push.chocolatey.org/ --api-key <key>
```

A new package waits for moderation (automatic checks, then a person),
usually a few days; updates of an approved package go faster.
