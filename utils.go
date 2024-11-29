package main

// StringInArray checks if a string exists in an array of strings
func StringInArray(target string, list []string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
