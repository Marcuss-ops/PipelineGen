package mediaexec

import (
	"fmt"
	"strings"
)

// CopyCertification is the copy-only certification of assembled segments —
// the Go side of the SHARED copy-safety contract the Rust
// AssemblyCompatibilityGate (pipelinegen-muscles assemble_copy,
// video.assemble.copy.v1) enforces per input and across inputs. The facts
// mirror the Rust wire type field-for-field (protocol.rs::CopyCertification);
// the gate admits a batch only when every segment is closed-GOP,
// first-frame-keyframe and stream-identical.
//
// This type lives in the media capability (not the platform executor) so
// every capability that dispatches copy assembly — videocreate's Assembler
// port included — speaks the certification without importing platform code.
type CopyCertification struct {
	CopyEligible          bool   `json:"copy_eligible"`
	ProfileID             string `json:"profile_id,omitempty"`
	Codec                 string `json:"codec,omitempty"`
	CodecProfile          string `json:"codec_profile,omitempty"`
	Width                 uint32 `json:"width,omitempty"`
	Height                uint32 `json:"height,omitempty"`
	FPSNum                uint32 `json:"fps_num,omitempty"`
	FPSDen                uint32 `json:"fps_den,omitempty"`
	ClosedGOP             bool   `json:"closed_gop"`
	FirstFrameKeyframe    bool   `json:"first_frame_keyframe"`
	ContractID            string `json:"contract_id,omitempty"`
	StreamSignatureSHA256 string `json:"stream_signature_sha256,omitempty"`
	VideoExtradataSHA256  string `json:"video_extradata_sha256,omitempty"`
	AudioExtradataSHA256  string `json:"audio_extradata_sha256,omitempty"`
}

// Validate mirrors the Rust CopyCertification::validate fail-closed gate so a
// non-certifiable batch is rejected before a Rust process starts.
func (c *CopyCertification) Validate() error {
	if c == nil {
		return fmt.Errorf("assemble_copy: copy_certification is required")
	}
	if !c.CopyEligible {
		return fmt.Errorf("assemble_copy: COPY_NOT_ELIGIBLE: copy_eligible is false")
	}
	if strings.TrimSpace(c.ProfileID) == "" {
		return fmt.Errorf("assemble_copy: CERTIFICATION_REQUIRED: profile_id is required")
	}
	if strings.TrimSpace(c.Codec) == "" {
		return fmt.Errorf("assemble_copy: CERTIFICATION_REQUIRED: codec is required")
	}
	if c.Width == 0 || c.Height == 0 {
		return fmt.Errorf("assemble_copy: CERTIFICATION_REQUIRED: width/height are required")
	}
	if c.FPSNum == 0 || c.FPSDen == 0 {
		return fmt.Errorf("assemble_copy: CERTIFICATION_REQUIRED: fps_num/fps_den are required")
	}
	if !c.ClosedGOP {
		return fmt.Errorf("assemble_copy: CERTIFICATION_REQUIRED: closed_gop must be true")
	}
	if !c.FirstFrameKeyframe {
		return fmt.Errorf("assemble_copy: CERTIFICATION_REQUIRED: first_frame_keyframe must be true")
	}
	return nil
}
