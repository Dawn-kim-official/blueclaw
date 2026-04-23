package adminapi

import (
	"encoding/json"
	"net/http"

	"blueclaw/internal/policy"
)

type PolicyHandler struct {
	PolicyPath    string
	PolicyLoader  policy.PolicyLoader
	PolicySaver   policy.PolicySaver
	PolicyWatcher *policy.PolicyWatcher
	Validator     policy.PolicyValidator
	AuditHandler  *AuditHandler
}

func (policyHandler PolicyHandler) HandleGetPolicy(responseWriter http.ResponseWriter, request *http.Request) {
	policyDocument, errorValue := policyHandler.PolicyLoader.LoadPolicyDocument(policyHandler.PolicyPath)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(responseWriter, http.StatusOK, policyDocument)
}

func (policyHandler PolicyHandler) HandleValidatePolicy(responseWriter http.ResponseWriter, request *http.Request) {
	var policyDocument policy.PolicyDocument
	errorValue := json.NewDecoder(request.Body).Decode(&policyDocument)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}

	errorValue = policyHandler.Validator.ValidatePolicyDocument(policyDocument)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(responseWriter, http.StatusOK, map[string]bool{"isValid": true})
}

func (policyHandler PolicyHandler) HandleSavePolicy(responseWriter http.ResponseWriter, request *http.Request) {
	var policyDocument policy.PolicyDocument
	errorValue := json.NewDecoder(request.Body).Decode(&policyDocument)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}

	errorValue = policyHandler.Validator.ValidatePolicyDocument(policyDocument)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}

	backupPath, _ := policyHandler.PolicySaver.BackupPolicyDocument(policyHandler.PolicyPath)
	errorValue = policyHandler.PolicySaver.SavePolicyDocumentAtomically(policyHandler.PolicyPath, policyDocument)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}

	policyHandler.PolicyWatcher.ReloadPolicyDocument(policyDocument)
	policyHandler.AuditHandler.RecordPolicySave(backupPath)
	writeJSON(responseWriter, http.StatusOK, map[string]string{"backupPath": backupPath})
}

func writeJSON(responseWriter http.ResponseWriter, statusCode int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	_ = json.NewEncoder(responseWriter).Encode(value)
}
