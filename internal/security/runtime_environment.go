package security

const CanonicalRuntimePATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func enforceCanonicalRuntimePATH(environmentVariables map[string]string) map[string]string {
	if environmentVariables == nil {
		environmentVariables = map[string]string{}
	}
	environmentVariables["PATH"] = CanonicalRuntimePATH
	return environmentVariables
}
