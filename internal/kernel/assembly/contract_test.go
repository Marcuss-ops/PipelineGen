package assembly

import "testing"

func TestPreparationHashIsDeterministicAndOrderIndependent(t *testing.T) {
	a := PrepareV1{ContractVersion: ContractVersion, AssemblyID: "a", ParentJobID: "p", OutputContract: OutputContract, Assets: []AssetRequirement{
		{AssetID: "b", Kind: "source_clip", SHA256: "2", Availability: AvailabilityKnown, Required: true},
		{AssetID: "a", Kind: "source_clip", SHA256: "1", Availability: AvailabilityKnown, Required: true},
	}}
	b := a
	b.Assets = []AssetRequirement{a.Assets[1], a.Assets[0]}
	ha, err := PreparationHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := PreparationHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("hash changed with asset ordering: %q != %q", ha, hb)
	}
}

func TestContractsFailClosedOnVersionAndOutput(t *testing.T) {
	p := PrepareV1{ContractVersion: "old", AssemblyID: "a", ParentJobID: "p", OutputContract: OutputContract, Assets: []AssetRequirement{{AssetID: "x", Kind: "clip"}}}
	if err := p.Validate(); err == nil {
		t.Fatal("old contract version accepted")
	}
	p.ContractVersion = ContractVersion
	p.OutputContract = "other"
	if err := p.Validate(); err == nil {
		t.Fatal("unknown media contract accepted")
	}
}
