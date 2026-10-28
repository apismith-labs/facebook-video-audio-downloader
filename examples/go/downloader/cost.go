package downloader

// Pricing captured from the Actor's build metadata on 2026-10-28.
// The Store headline ("$1.00 / 1,000 results") covers only DatasetItemUSD.
const DatasetItemUSD = 0.001

// TierPricesUSD is the transcription price per started minute, by Apify plan.
var TierPricesUSD = map[string]float64{
	"FREE":     0.0045,
	"BRONZE":   0.0035,
	"SILVER":   0.003,
	"GOLD":     0.002,
	"PLATINUM": 0.002,
	"DIAMOND":  0.002,
}

// Tiers lists the accepted tier names in a stable order.
var Tiers = []string{"FREE", "BRONZE", "SILVER", "GOLD", "PLATINUM", "DIAMOND"}

// BilledMinutes applies the one-minute floor: any duration under a minute is
// billed as a full minute.
func BilledMinutes(seconds float64) int {
	if seconds <= 0 {
		return 1
	}
	minutes := int(seconds) / 60
	if int(seconds)%60 != 0 || seconds != float64(int(seconds)) {
		minutes++
	}
	if minutes < 1 {
		minutes = 1
	}
	return minutes
}
