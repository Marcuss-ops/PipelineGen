package sqlutil

import "strings"

func BuildFallbackLikeConditions(tokens []string, columns []string) (string, []interface{}) {
	if len(tokens) == 0 || len(columns) == 0 {
		return "", nil
	}

	var andConditions []string
	var args []interface{}

	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if len(token) < 2 {
			continue
		}

		var colConditions []string
		for _, col := range columns {
			colConditions = append(colConditions, col+" LIKE ?")
			args = append(args, "%"+token+"%")
		}

		andConditions = append(andConditions, "("+strings.Join(colConditions, " OR ")+")")
	}

	if len(andConditions) == 0 {
		return "", nil
	}

	return "(" + strings.Join(andConditions, " AND ") + ")", args
}
