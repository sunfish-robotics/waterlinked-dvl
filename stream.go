package dvl

type reportStream[T any] struct {
	input  chan T
	output chan Sample[T]

	// capacity is how many reports the broker retains before dropping the
	// oldest. The dialer resolves it once, so it is fixed for the connection.
	capacity int
}

func newReportStream[T any](capacity int) reportStream[T] {
	return reportStream[T]{
		input:    make(chan T),
		output:   make(chan Sample[T]),
		capacity: capacity,
	}
}

func (s *reportStream[T]) publish(done <-chan struct{}, report T) bool {
	select {
	case s.input <- report:
		return true
	case <-done:
		return false
	}
}

func (s *reportStream[T]) run(done <-chan struct{}) {
	defer close(s.output)

	queue := make([]T, s.capacity)
	head := 0
	size := 0
	var dropped uint64

	for {
		select {
		case <-done:
			return
		default:
		}

		var output chan Sample[T]
		var sample Sample[T]
		if size != 0 {
			output = s.output
			sample = Sample[T]{Report: queue[head], DroppedBefore: dropped}
		}

		select {
		case <-done:
			return
		case report := <-s.input:
			if size == len(queue) {
				var zero T
				queue[head] = zero
				head = (head + 1) % len(queue)
				size--
				dropped++
			}
			queue[(head+size)%len(queue)] = report
			size++
		case output <- sample:
			var zero T
			queue[head] = zero
			head = (head + 1) % len(queue)
			size--
			dropped = 0
		}
	}
}
