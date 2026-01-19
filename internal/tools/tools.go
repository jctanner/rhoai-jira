package tools

import (
	"sort"
	"strconv"
	"strings"
)

func ItemInList(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func SortNumerically(inList []string) []string {
	// sort a list of ABDCD-XXXX strings
	sort.Slice(inList, func(i, j int) bool {
		_, iNumStr, _ := strings.Cut(inList[i], "-")
		_, jNumStr, _ := strings.Cut(inList[j], "-")

		iNum, err1 := strconv.Atoi(iNumStr)
		jNum, err2 := strconv.Atoi(jNumStr)

		// Fallback to lexical if parsing fails
		if err1 != nil || err2 != nil {
			return inList[i] < inList[j]
		}
		return iNum < jNum
	})
	return inList
}

func FilterByIndexValue(listOfLists [][]string, index int, match string) [][]string {
	var result [][]string
	for _, sublist := range listOfLists {
		if len(sublist) > 0 && sublist[index] == match {
			result = append(result, sublist)
		}
	}
	return result
}
