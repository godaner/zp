package zpp
type Attr interface {
	T() byte
	L() uint16
	V() []byte
}