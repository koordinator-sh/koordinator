package system

const (
	GoldNetClsID   = 0xab5a2010 // net_priority(3)
	GoldDSCP       = 18
	SilverNetClsID = 0xab5a2020 // net_priority(5)
	SilverDSCP     = 17
	CopperNetClsID = 0xab5a2030 // net_priority(7)
	CopperDSCP     = 16
)

type BandwidthConfig struct {
	GatewayIfaceName string
	GatewayLimit     uint64

	RootLimit uint64

	GoldRequest uint64
	GoldLimit   uint64
	GoldDSCP    uint64

	SilverRequest uint64
	SilverLimit   uint64
	SilverDSCP    uint64

	CopperRequest uint64
	CopperLimit   uint64
	CopperDSCP    uint64
}
