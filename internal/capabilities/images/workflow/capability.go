package workflow

// Capability enumerates image-domain features advertised by the API.
// AI image generation has one capability only: Google Slides driven through
// the Chrome/Playwright adapter.
type Capability string

const (
	// CapImageGenChrome mirrors the RequiredCapabilities entry used by the
	// image.generate.google job. Chrome/Playwright is the implementation driver;
	// Google Slides is the sole generation backend.
	CapImageGenChrome Capability = "image_gen_chrome"
)

// CapabilityStatus is the explicit availability state used by HTTP handlers.
type CapabilityStatus string

const (
	StatusAvailable         CapabilityStatus = "available"
	StatusNotImplemented    CapabilityStatus = "not_implemented"
	StatusMissingDependency CapabilityStatus = "missing_dependency"
)
