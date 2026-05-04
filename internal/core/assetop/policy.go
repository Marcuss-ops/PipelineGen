package assetop

type Policy struct {
	Existing     string `json:"existing" yaml:"existing"` // verify, skip, replace
	UploadDrive  bool   `json:"upload_drive" yaml:"upload_drive"`
	SaveDB       bool   `json:"save_db" yaml:"save_db"`
	RequireHash  bool   `json:"require_hash" yaml:"require_hash"`
	RequireLocal bool   `json:"require_local" yaml:"require_local"`
	RequireDrive bool   `json:"require_drive" yaml:"require_drive"`
}
