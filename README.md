# Relical

Mail backup and archiving for hosting servers, built on restic. A panel-neutral core (`core/`) with thin control-panel adapters (`panels/`, starting with Plesk). Part of the Noiz Déjà Vu backup range.

Status: phase 1 prototype (mailbox backup and restore). Not ready for production.

## Build and test

    make tools        # pinned restic 0.19.1 and rest-server 0.14.0 into .tools/
    make test         # unit tests
    make integration  # against real restic and an append-only rest-server
    make e2e          # backup and restore through a real Dovecot in Docker
    make build        # dist/relical, static

Licensed GPLv3.
