package netcontrol

// FakeController lets tests exercise EvaluateAndHeal's decision logic
// without touching the real device - set the fields to whatever
// scenario you want to simulate, then assert on ReconnectCalls.
type FakeController struct {
	Signal         SignalInfo
	SignalErr      error
	ReconnectErr   error
	ReconnectCalls int
}

func (f *FakeController) GetSignalInfo() (SignalInfo, error) {
	return f.Signal, f.SignalErr
}

func (f *FakeController) ReconnectCellular() error {
	f.ReconnectCalls++
	return f.ReconnectErr
}
