package model

import "github.com/Marcuss-ops/RenderingGen/queue/client"

// Artifact is the metadata of a rendered artifact, produced by a worker on job
// completion and persisted to render_artifacts. It carries the copy-only
// certification (codec, profile, GOP/keyframe flags) that VeloxEditing relies
// on to assemble an overlay without re-decoding or re-encoding it.
//
// It is an ALIAS of the public wire contract type (queue/client), exactly like
// State and the JobSchema constants: there is ONE artifact definition, so a
// field can be added to the wire (or the persistence model) in exactly one
// place and the two sides can never silently diverge. The historical full
// re-declaration here — kept in step only by a hand-maintained reflection
// parity test — is gone.
type Artifact = client.Artifact

// OutputFacts is an alias of the wire contract's complete structural
// certification (see client.OutputFacts): one definition, so the persisted
// JSON and the emitted JSON can never diverge.
type OutputFacts = client.OutputFacts

// ArtifactProvenance is an alias of the wire contract's provenance record
// (see client.ArtifactProvenance): how artifact bytes entered the certified
// artifact store, persisted as the render_artifacts.provenance JSONB column.
type ArtifactProvenance = client.ArtifactProvenance
