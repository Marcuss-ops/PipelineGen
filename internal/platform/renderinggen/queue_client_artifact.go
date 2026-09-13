// queue_client_artifact.go owns the artifact projection between the queue's
// wire type and the script capability's projection, including the verbatim
// relay of RenderingGen's complete structural certification (output_facts).
//
// Split out of queue_client.go to stay under the strict per-file line cap;
// the behavior is unchanged.
package renderinggen

import (
	"encoding/json"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

func toScriptArtifact(in *queueclient.Artifact) *scriptgen.RenderArtifact {
	if in == nil {
		return nil
	}
	return &scriptgen.RenderArtifact{
		ID:                 in.ID,
		Kind:               in.Kind,
		StorageKey:         in.StorageKey,
		URL:                in.ArtifactURL,
		SHA256:             in.ArtifactHash,
		MimeType:           in.ContentType,
		SizeBytes:          in.SizeBytes,
		Width:              in.Width,
		Height:             in.Height,
		FPSNum:             in.FPSNum,
		FPSDen:             in.FPSDen,
		FrameCount:         in.FrameCount,
		DurationUS:         in.DurationUS,
		ProfileID:          in.ProfileID,
		CopyEligible:       in.CopyEligible,
		Codec:              in.Codec,
		CodecProfile:       in.CodecProfile,
		Container:          in.Container,
		PixelFormat:        in.PixelFormat,
		AudioStreams:       in.AudioStreams,
		OutputFacts:        rawOutputFacts(in.OutputFacts),
		ClosedGOP:          in.ClosedGOP,
		FirstFrameKeyframe: in.FirstFrameKeyframe,
		RenderMS:           metricMillis(in.Metrics, "render_ms"),
		EncodeMS:           metricMillis(in.Metrics, "encode_ms"),
		MaterializeMS:      metricMillisEither(in.Metrics, "materialize_ms", "materialize_us"),
		PlanMS:             metricMillisEither(in.Metrics, "overlay_compile_ms", "overlay_compile_us"),
		ProbeMS:            metricMillisEither(in.Metrics, "probe_ms", "probe_us"),
		HashMS:             metricMillisEither(in.Metrics, "sha256_ms", "sha256_us"),
		UploadMS:           metricMillisEither(in.Metrics, "objectstore_upload_ms", "objectstore_upload_us"),
		DrivePublishMS:     metricMillisEither(in.Metrics, "drive_publish_ms", "drive_upload_us"),
		Backend:            in.Backend,
		ChrononVersion:     in.ChrononVersion,
		DriveFileID:        in.DriveFileID,
		DriveLink:          in.DriveLink,
		Metrics:            in.Metrics,
		// Raw deep-profile sidecar reference (content-addressed preservation).
		ChrononTimingStorageKey:  in.ChrononTimingStorageKey,
		ChrononTimingURL:         in.ChrononTimingURL,
		ChrononTimingSHA256:      in.ChrononTimingSHA256,
		ChrononTimingSizeBytes:   in.ChrononTimingSizeBytes,
		ChrononTimingContentType: in.ChrononTimingContentType,
	}
}

// rawOutputFacts relays the queue's complete structural certification verbatim.
// The fact set is owned by the rendering boundary, so this projection must not
// re-declare its shape: it marshals the wire object into raw JSON that the
// consumer decodes into its own capability-local struct. A nil fact set stays
// nil (a legacy worker), never an empty object.
func rawOutputFacts(facts *queueclient.OutputFacts) json.RawMessage {
	if facts == nil {
		return nil
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return nil
	}
	return raw
}

// decodeCertifiedFacts decodes the raw certification relayed by the script
// artifact into the capability-local fact set. Absent (nil) means the boundary
// certified only the flat summary, which is valid; malformed is an error.
func decodeCertifiedFacts(raw json.RawMessage) (*cliprender.OutputFacts, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var facts cliprender.OutputFacts
	if err := json.Unmarshal(raw, &facts); err != nil {
		return nil, err
	}
	return &facts, nil
}
