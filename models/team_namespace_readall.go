package models

func (tn *TeamNamespace) ReadAll(user *User) (interface{}, error) {
	// Check if the user can read the namespace
	n, err := GetNamespaceByID(tn.NamespaceID)
	if err != nil {
		return nil, err
	}
	if !n.CanRead(user) {
		return nil, ErrNeedToHaveNamespaceReadAccess{NamespaceID:tn.NamespaceID, UserID:user.ID}
	}

	// Get the teams
	all := []*Team{}

	err = x.Select("teams.*").
		Table("teams").
		Join("INNER", "team_namespaces", "team_id = teams.id").
		Where("team_namespaces.namespace_id = ?", tn.NamespaceID).
		Find(&all)

	return all, err
}