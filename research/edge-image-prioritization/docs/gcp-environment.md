# Ghost Seeker GCP environment

This is a portable environment guide for the Ghost Seeker research project, not an authentication bundle. The original reusable Codex skill is preserved privately as `ghost-seeker-gcp.zip` in the [artifact archive](artifacts.md). Never copy lab credentials or account defaults into Git.

The research used project `qwiklabs-gcp-02-34a870014566` and bucket `qwiklabs-gcp-02-34a870014566-ghost-seeker`. These are historical project identifiers, not guarantees that a temporary lab still exists. Before work, verify current authentication and project visibility with `gcloud auth list`, `gcloud projects describe PROJECT_ID`, and read-only object listing. Keep credential-bearing command output private. Use explicit `--project` arguments and task-local caches rather than changing global defaults.

The preserved output prefix is `research/codex/payload-20261105-acc4/artifacts/`. Read its inventory and hashes before downloading exact required objects. Keep repaired source datasets and catalogs read-only. Catalog annotations may be incomplete; dataset status, grouping, coordinate systems and source licenses must remain part of the evaluation design.

For new authorized experiments, use a unique research prefix and an owned, explicitly named compute resource. Record its identity, input object generations, code hashes, runtime configuration, actual command, result manifest and cleanup evidence. Never operate on a resource merely because it looks temporary. Confirm ownership before deleting the experiment VM and boot disk, preserve results first, and verify deletion afterward. The completed CPU follow-up's recorded cleanup is in `../evidence/phase2/cpu-cleanup.json`.

Production buckets, IAM, shared instances, catalogs and the source repository require their own task authorization. Keep agent code and results in an isolated checkout; this guide does not authorize cloud deployment, new resource creation, or changes to active developer work. Set a bounded runtime and explicit machine size for each experiment even when credits are plentiful: the limit makes completion and cleanup verifiable.
