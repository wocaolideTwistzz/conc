package conc

type Result[T any] struct {
	Ret T
	Err error
}
