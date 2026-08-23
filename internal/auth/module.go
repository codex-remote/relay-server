package auth

import "net/http"

// Module is the HTTP integration boundary for Runtime authentication. Business
// route policy is supplied by the host so Auth does not depend on Runtime URLs.
type Module struct {
	service *Service
	public  *PublicAPI
	control http.Handler
}

func NewModule(service *Service, config HTTPConfig) *Module {
	return &Module{
		service: service,
		public:  NewPublicAPI(service, config),
		control: NewControlAPI(service).Handler(),
	}
}

func (m *Module) RegisterPublic(mux *http.ServeMux) {
	m.public.Register(mux)
}

func (m *Module) Protect(next http.Handler, requiredScope func(*http.Request) string) http.Handler {
	return m.service.Middleware(next, requiredScope)
}

func (m *Module) ControlHandler() http.Handler {
	return m.control
}
