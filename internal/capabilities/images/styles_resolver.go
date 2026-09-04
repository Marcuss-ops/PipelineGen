package images

import imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"

// Root compatibility aliases. Canonical ownership lives in images/styles.
type SourceBackend = imagestyles.SourceBackend
type StyleSnapshot = imagestyles.StyleSnapshot
type StyleResolver = imagestyles.StyleResolver

const DefaultStyleID = imagestyles.DefaultStyleID

func New(source SourceBackend) StyleResolver { return imagestyles.New(source) }
