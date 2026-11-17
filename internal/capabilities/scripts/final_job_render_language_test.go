package scriptgeneration

import "testing"

func TestFinalJobRenderLanguageUsesSingletonTargetBeforeLocalizedRenderExists(t *testing.T) {
	got, err := finalJobRenderLanguage(GenerateRequest{
		SourceLanguage: "it", Languages: []Language{"pt"},
	}, &GenerateResult{})
	if err != nil {
		t.Fatalf("finalJobRenderLanguage: %v", err)
	}
	if got != "pt" {
		t.Fatalf("render language = %q, want target pt before fixed-media subtitle render", got)
	}
}

func TestFinalJobRenderLanguagePrefersRequestedTarget(t *testing.T) {
	got, err := finalJobRenderLanguage(GenerateRequest{
		SourceLanguage: "it", Languages: []Language{"ru"},
	}, &GenerateResult{LocalizedRenders: []LocalizedRenderResult{
		{Language: "it"}, {Language: "ru"},
	}})
	if err != nil {
		t.Fatalf("finalJobRenderLanguage: %v", err)
	}
	if got != "ru" {
		t.Fatalf("render language = %q, want target ru", got)
	}
}
