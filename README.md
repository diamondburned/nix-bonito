# nix-bonito

## Usage

To be implemented.

nix-bonito is meant to be used with a VCS such as Git. As a result, changes to
the configuration file and its checksum files should be committed so that they
can be rolled back using the VCS.

```sh
# Initialize with config
bonito # uses $HOSTNAME.toml
bonito -c hackadoll3.toml
# Update locks only
bonito --update-locks
# Update locks and channels
bonito -u
```
