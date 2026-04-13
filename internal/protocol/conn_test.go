package protocol

import (
	"testing"
	"time"
)

func TestDeliverFrameWaitsForRegularBufferDrain(t *testing.T) {
	c := &Conn{
		regularCh: make(chan *Frame, 1),
		controlCh: make(chan *Frame, 1),
		closeCh:   make(chan struct{}),
	}
	c.regularCh <- &Frame{Type: MessageRegular, ID: 1}

	done := make(chan struct{})
	go func() {
		_ = c.deliverFrame(&Frame{Type: MessageRegular, ID: 2})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("deliverFrame returned before the buffer drained")
	case <-time.After(50 * time.Millisecond):
	}

	<-c.regularCh

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deliverFrame stayed blocked after the buffer drained")
	}

	frame := <-c.regularCh
	if frame.ID != 2 {
		t.Fatalf("frame ID = %d, want 2", frame.ID)
	}
}

func TestDeliverFrameReturnsWhenConnectionCloses(t *testing.T) {
	c := &Conn{
		regularCh: make(chan *Frame, 1),
		controlCh: make(chan *Frame, 1),
		closeCh:   make(chan struct{}),
	}
	c.regularCh <- &Frame{Type: MessageRegular, ID: 1}

	done := make(chan error, 1)
	go func() {
		done <- c.deliverFrame(&Frame{Type: MessageRegular, ID: 2})
	}()

	close(c.closeCh)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected deliverFrame to stop on close")
		}
	case <-time.After(time.Second):
		t.Fatal("deliverFrame did not stop when the connection closed")
	}
}
