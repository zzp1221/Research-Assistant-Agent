//go:build !windows

package procgroup

// Job is a non-Windows stub so callers can compile the same supervisor code.
type Job struct{}

// NewJob always reports ErrUnsupported outside Windows.
func NewJob(Limits) (*Job, error) {
	return nil, ErrUnsupported
}

func (j *Job) Assign(process interface{}) error { return ErrUnsupported }
func (j *Job) Terminate() error                 { return ErrUnsupported }
func (j *Job) Close() error                     { return nil }
