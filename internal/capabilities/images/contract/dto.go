package images

type ImageSearchTerritory string

const (
	TerritoryRetrieved ImageSearchTerritory = "retrieved"
	TerritoryGenerated ImageSearchTerritory = "generated"
	TerritoryAll       ImageSearchTerritory = "all"
)

func (t ImageSearchTerritory) IsValid() bool {
	switch t {
	case TerritoryRetrieved, TerritoryGenerated, TerritoryAll:
		return true
	default:
		return false
	}
}
