package blazeSDK

import (
	"fmt"
	"sort"
	"strings"
)

// writeValue - prints a formatted log of the TDF payload
func writeValue(payload interface{}, indent int) {
	pad := strings.Repeat("  ", indent)
	switch payloadType := payload.(type) {

	case map[string]interface{}: // struct (TDF)
		fmt.Print("{\n")
		for _, key := range sortedKeys(payloadType) {
			fmt.Printf("%s  %s = ", pad, key)
			writeValue(payloadType[key], indent+1)
			fmt.Println()
		}
		fmt.Printf("%s}", pad)

	case []interface{}: // list
		fmt.Print("[\n")
		for i, key := range payloadType {
			fmt.Printf("%s  [%d] = ", pad, i)
			writeValue(key, indent+1)
			fmt.Println()
		}
		fmt.Printf("%s]", pad)

	case map[interface{}]interface{}: // map
		fmt.Print("[\n")
		for _, key := range sortedMapKeys(payloadType) {
			fmt.Printf("%s  (%s, ", pad, scalar(key))
			writeValue(payloadType[key], indent+1)
			fmt.Print(")\n")
		}
		fmt.Printf("%s]", pad)

	default: // scalar
		fmt.Print(scalar(payload))
	}
}

// scalar - prints the formatted output of the basic TDF types
func scalar(v interface{}) string {
	switch x := v.(type) {
	case int64:
		return fmt.Sprintf("%d (0x%04X)", x, uint64(x))
	case string:
		return fmt.Sprintf("%q", x)
	case []byte:
		return fmt.Sprintf("0x%X", x) // blob
	case float32:
		return fmt.Sprintf("%g", x)
	default: // ObjectType/ObjectID/Union/Variable
		return fmt.Sprintf("%v", x)
	}
}

// sortedKeys - sorts the values of a LIST
func sortedKeys(input map[string]interface{}) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedKeys - sorts the values of a MAP
func sortedMapKeys(input map[interface{}]interface{}) []interface{} {
	keys := make([]interface{}, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return fmt.Sprintf("%v", keys[i]) < fmt.Sprintf("%v", keys[j])
	})
	return keys
}
