/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package common contains utility functions used by different components
package common

import (
	"fmt"
	"os"
	"regexp"
)

// ReplaceInFile replaces content in the given file either by plain strings or regex patterns based on the content.
func ReplaceInFile(fileName string, replacements map[string]string) error {
	// Read the contents of the file
	fileContent, err := os.ReadFile(fileName)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Replace content using string or regex
	newContent := string(fileContent)
	for pattern, replacement := range replacements {
		regexPattern, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("failed to compile pattern: %w", err)
		}
		newContent = regexPattern.ReplaceAllString(newContent, replacement)
	}

	// Write the modified content back to the file
	err = os.WriteFile(fileName, []byte(newContent), 0)
	if err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	return nil
}
