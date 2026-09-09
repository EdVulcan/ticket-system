package service

// Notifications only accelerate the durable task queue. A full channel already
// represents a pending wake; missed signals are recovered by the periodic scan.
var digitalRefundWakeups = make(chan struct{}, 1)

func DigitalRefundWakeups() <-chan struct{} { return digitalRefundWakeups }

func notifyDigitalRefundWorker() {
	select {
	case digitalRefundWakeups <- struct{}{}:
	default:
	}
}
