package server

// goBackground runs fn on its own goroutine.
func (s *Server) goBackground(name string, fn func()) {
	go fn()
}
