package assetop

type Destination struct {
	Group           string `json:"group" yaml:"group"`
	FolderID        string `json:"folder_id" yaml:"folder_id"`
	FolderPath      string `json:"folder_path" yaml:"folder_path"`
	SubfolderName   string `json:"subfolder_name" yaml:"subfolder_name"`
	CreateSubfolder bool   `json:"create_subfolder" yaml:"create_subfolder"`
}
