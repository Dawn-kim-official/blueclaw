package policy

import "sync"

type PolicyWatcher struct {
	mutex          sync.RWMutex
	policyDocument PolicyDocument
}

func (policyWatcher *PolicyWatcher) ReloadPolicyDocument(policyDocument PolicyDocument) {
	policyWatcher.mutex.Lock()
	defer policyWatcher.mutex.Unlock()
	policyWatcher.policyDocument = policyDocument
}

func (policyWatcher *PolicyWatcher) CurrentPolicyDocument() PolicyDocument {
	policyWatcher.mutex.RLock()
	defer policyWatcher.mutex.RUnlock()
	return policyWatcher.policyDocument
}
