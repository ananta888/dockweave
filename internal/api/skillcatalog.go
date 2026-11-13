package api

import (
	"encoding/json"
	"net/http"
)

// skillCatalog lists bundled, installed and curated store skills.
func (s *Server) skillCatalog(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusNotImplemented, "not_implemented", errSkillsUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, s.Skills.Snapshot())
}

// installCatalogSkill downloads one skill folder from an allowlisted store.
func (s *Server) installCatalogSkill(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusNotImplemented, "not_implemented", errSkillsUnavailable)
		return
	}
	var req struct {
		Repo string `json:"repo"`
		Path string `json:"path"`
		Ref  string `json:"ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	entry, err := s.Skills.Install(r.Context(), req.Repo, req.Path, req.Ref)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "skill_install_failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

// deleteCatalogSkill removes an installed skill from the managed directory.
func (s *Server) deleteCatalogSkill(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusNotImplemented, "not_implemented", errSkillsUnavailable)
		return
	}
	if err := s.Skills.Delete(r.PathValue("name")); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errSkillsUnavailable = &staticError{"skill catalog is not configured on this daemon"}
