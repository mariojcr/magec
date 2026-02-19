package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"

	"github.com/achetronic/magec/server/store"
)

// listSkills returns all skills.
// @Summary      List skills
// @Description  Returns all configured skills
// @Tags         skills
// @Produce      json
// @Success      200  {array}  store.Skill
// @Security     AdminAuth
// @Router       /skills [get]
func (h *Handler) listSkills(w http.ResponseWriter, r *http.Request) {
	skills := h.store.ListRawSkills()
	writeJSON(w, http.StatusOK, skills)
}

// getSkill returns a single skill by ID.
// @Summary      Get skill
// @Description  Returns a skill by its unique ID
// @Tags         skills
// @Produce      json
// @Param        id    path      string  true  "Skill ID"
// @Success      200   {object}  store.Skill
// @Failure      404   {object}  ErrorResponse
// @Security     AdminAuth
// @Router       /skills/{id} [get]
func (h *Handler) getSkill(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	sk, ok := h.store.GetRawSkill(id)
	if !ok {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}
	writeJSON(w, http.StatusOK, sk)
}

// createSkill creates a new skill.
// @Summary      Create skill
// @Description  Creates a new skill with instructions and optional references
// @Tags         skills
// @Accept       json
// @Produce      json
// @Param        body  body      store.Skill  true  "Skill definition"
// @Success      201   {object}  store.Skill
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Security     AdminAuth
// @Router       /skills [post]
func (h *Handler) createSkill(w http.ResponseWriter, r *http.Request) {
	var sk store.Skill
	if err := json.NewDecoder(r.Body).Decode(&sk); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if sk.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if sk.Instructions == "" {
		writeError(w, http.StatusBadRequest, "instructions are required")
		return
	}
	sk.References = nil
	created, err := h.store.CreateSkill(sk)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// updateSkill updates an existing skill.
// @Summary      Update skill
// @Description  Updates a skill by ID
// @Tags         skills
// @Accept       json
// @Produce      json
// @Param        id    path      string       true  "Skill ID"
// @Param        body  body      store.Skill  true  "Skill definition"
// @Success      200   {object}  store.Skill
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Security     AdminAuth
// @Router       /skills/{id} [put]
func (h *Handler) updateSkill(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var sk store.Skill
	if err := json.NewDecoder(r.Body).Decode(&sk); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	existing, ok := h.store.GetRawSkill(id)
	if !ok {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}
	sk.References = existing.References
	if err := h.store.UpdateSkill(id, sk); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	updated, _ := h.store.GetRawSkill(id)
	writeJSON(w, http.StatusOK, updated)
}

// deleteSkill deletes a skill.
// @Summary      Delete skill
// @Description  Deletes a skill by ID
// @Tags         skills
// @Param        id  path  string  true  "Skill ID"
// @Success      204
// @Failure      404  {object}  ErrorResponse
// @Security     AdminAuth
// @Router       /skills/{id} [delete]
func (h *Handler) deleteSkill(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.store.DeleteSkill(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// uploadSkillReference uploads a file as a skill reference.
// @Summary      Upload skill reference
// @Description  Uploads a file and registers it as a reference for the skill
// @Tags         skills
// @Accept       multipart/form-data
// @Produce      json
// @Param        id    path      string  true  "Skill ID"
// @Param        file  formData  file    true  "Reference file"
// @Success      201   {object}  store.SkillReference
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Security     AdminAuth
// @Router       /skills/{id}/references [post]
func (h *Handler) uploadSkillReference(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	if _, ok := h.store.GetSkill(id); !ok {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}

	r.ParseMultipartForm(10 << 20) // 10 MB limit
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	filename := filepath.Base(header.Filename)
	if filename == "" || filename == "." {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}
	if strings.Contains(filename, "..") {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	dir := h.store.SkillDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create directory: %v", err))
		return
	}

	dst, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create file: %v", err))
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to write file: %v", err))
		return
	}

	ref := store.SkillReference{
		Filename: filename,
		Size:     written,
	}

	if err := h.store.AddSkillReference(id, ref); err != nil {
		os.Remove(filepath.Join(dir, filename))
		if strings.Contains(err.Error(), "already exists") {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusNotFound, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusCreated, ref)
}

// downloadSkillReference serves a skill reference file.
// @Summary      Download skill reference
// @Description  Downloads a reference file from a skill
// @Tags         skills
// @Produce      octet-stream
// @Param        id        path  string  true  "Skill ID"
// @Param        filename  path  string  true  "Reference filename"
// @Success      200
// @Failure      404  {object}  ErrorResponse
// @Security     AdminAuth
// @Router       /skills/{id}/references/{filename} [get]
func (h *Handler) downloadSkillReference(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	filename := vars["filename"]

	if _, ok := h.store.GetSkill(id); !ok {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}

	if strings.Contains(filename, "..") || strings.Contains(filename, "/") {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	path := filepath.Join(h.store.SkillDir(id), filename)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "reference file not found")
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	http.ServeFile(w, r, path)
}

// deleteSkillReference deletes a skill reference file.
// @Summary      Delete skill reference
// @Description  Removes a reference file from a skill
// @Tags         skills
// @Param        id        path  string  true  "Skill ID"
// @Param        filename  path  string  true  "Reference filename"
// @Success      204
// @Failure      404  {object}  ErrorResponse
// @Security     AdminAuth
// @Router       /skills/{id}/references/{filename} [delete]
func (h *Handler) deleteSkillReference(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	filename := vars["filename"]

	if strings.Contains(filename, "..") || strings.Contains(filename, "/") {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	if err := h.store.RemoveSkillReference(id, filename); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	path := filepath.Join(h.store.SkillDir(id), filename)
	os.Remove(path)

	w.WriteHeader(http.StatusNoContent)
}
