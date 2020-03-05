package endpoint

type Status byte

const (
	Status_Stoped    = iota
	Status_Started   = iota
	Status_Destroied = iota
)

type Endpoint interface {
	Start() error
	Stop() error
	Restart() error
	Status() Status
	Destroy() error
	GetID() uint16
}
