// Vikunja is a to-do list application to facilitate your life.
// Copyright 2018-2020 Vikunja and contributors. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package models

import (
	"time"

	"code.vikunja.io/api/pkg/user"
	"code.vikunja.io/web"
	"xorm.io/xorm"
)

// TaskAssginee represents an assignment of a user to a task
type TaskAssginee struct {
	ID      int64     `xorm:"bigint autoincr not null unique pk" json:"-"`
	TaskID  int64     `xorm:"bigint INDEX not null" json:"-" param:"listtask"`
	UserID  int64     `xorm:"bigint INDEX not null" json:"user_id" param:"user"`
	Created time.Time `xorm:"created not null"`

	web.CRUDable `xorm:"-" json:"-"`
	web.Rights   `xorm:"-" json:"-"`
}

// TableName makes a pretty table name
func (TaskAssginee) TableName() string {
	return "task_assignees"
}

// TaskAssigneeWithUser is a helper type to deal with user joins
type TaskAssigneeWithUser struct {
	TaskID    int64
	user.User `xorm:"extends"`
}

func getRawTaskAssigneesForTasks(taskIDs []int64) (taskAssignees []*TaskAssigneeWithUser, err error) {
	taskAssignees = []*TaskAssigneeWithUser{}
	err = x.Table("task_assignees").
		Select("task_id, users.*").
		In("task_id", taskIDs).
		Join("INNER", "users", "task_assignees.user_id = users.id").
		Find(&taskAssignees)
	return
}

// Create or update a bunch of task assignees
func (t *Task) updateTaskAssignees(s *xorm.Session, assignees []*user.User) (err error) {

	// Load the current assignees
	currentAssignees, err := getRawTaskAssigneesForTasks([]int64{t.ID})
	if err != nil {
		return err
	}

	t.Assignees = make([]*user.User, 0, len(currentAssignees))
	for _, assignee := range currentAssignees {
		t.Assignees = append(t.Assignees, &assignee.User)
	}

	// If we don't have any new assignees, delete everything right away. Saves us some hassle.
	if len(assignees) == 0 && len(t.Assignees) > 0 {
		_, err = s.Where("task_id = ?", t.ID).
			Delete(TaskAssginee{})
		t.setTaskAssignees(assignees)
		return err
	}

	// If we didn't change anything (from 0 to zero) don't do anything.
	if len(assignees) == 0 && len(t.Assignees) == 0 {
		return nil
	}

	// Make a hashmap of the new assignees for easier comparison
	newAssignees := make(map[int64]*user.User, len(assignees))
	for _, newAssignee := range assignees {
		newAssignees[newAssignee.ID] = newAssignee
	}

	// Get old assignees to delete
	var found bool
	var assigneesToDelete []int64
	oldAssignees := make(map[int64]*user.User, len(t.Assignees))
	for _, oldAssignee := range t.Assignees {
		found = false
		if newAssignees[oldAssignee.ID] != nil {
			found = true // If a new assignee is already in the list with old assignees
		}

		// Put all assignees which are only on the old list to the trash
		if !found {
			assigneesToDelete = append(assigneesToDelete, oldAssignee.ID)
		}

		oldAssignees[oldAssignee.ID] = oldAssignee
	}

	// Delete all assignees not passed
	if len(assigneesToDelete) > 0 {
		_, err = s.In("user_id", assigneesToDelete).
			And("task_id = ?", t.ID).
			Delete(TaskAssginee{})
		if err != nil {
			return err
		}
	}

	// Get the list to perform later checks
	list := List{ID: t.ListID}
	err = list.GetSimpleByID()
	if err != nil {
		return
	}

	// Loop through our users and add them
	for _, u := range assignees {
		// Check if the user is already assigned and assign him only if not
		if oldAssignees[u.ID] != nil {
			// continue outer loop
			continue
		}

		// Add the new assignee
		err = t.addNewAssigneeByID(u.ID, &list)
		if err != nil {
			return err
		}
	}

	t.setTaskAssignees(assignees)

	err = updateListLastUpdated(&List{ID: t.ListID})
	return
}

// Small helper functions to set the new assignees in various places
func (t *Task) setTaskAssignees(assignees []*user.User) {
	if len(assignees) == 0 {
		t.Assignees = nil
		return
	}
	t.Assignees = assignees
}

// Delete a task assignee
// @Summary Delete an assignee
// @Description Un-assign a user from a task.
// @tags assignees
// @Accept json
// @Produce json
// @Security JWTKeyAuth
// @Param taskID path int true "Task ID"
// @Param userID path int true "Assignee user ID"
// @Success 200 {object} models.Message "The assignee was successfully deleted."
// @Failure 403 {object} web.HTTPError "Not allowed to delete the assignee."
// @Failure 500 {object} models.Message "Internal error"
// @Router /tasks/{taskID}/assignees/{userID} [delete]
func (la *TaskAssginee) Delete() (err error) {
	_, err = x.Delete(&TaskAssginee{TaskID: la.TaskID, UserID: la.UserID})
	if err != nil {
		return err
	}

	err = updateListByTaskID(la.TaskID)
	return
}

// Create adds a new assignee to a task
// @Summary Add a new assignee to a task
// @Description Adds a new assignee to a task. The assignee needs to have access to the list, the doer must be able to edit this task.
// @tags assignees
// @Accept json
// @Produce json
// @Security JWTKeyAuth
// @Param assignee body models.TaskAssginee true "The assingee object"
// @Param taskID path int true "Task ID"
// @Success 200 {object} models.TaskAssginee "The created assingee object."
// @Failure 400 {object} web.HTTPError "Invalid assignee object provided."
// @Failure 500 {object} models.Message "Internal error"
// @Router /tasks/{taskID}/assignees [put]
func (la *TaskAssginee) Create(a web.Auth) (err error) {

	// Get the list to perform later checks
	list, err := GetListSimplByTaskID(la.TaskID)
	if err != nil {
		return
	}

	task := &Task{ID: la.TaskID}
	return task.addNewAssigneeByID(la.UserID, list)
}

func (t *Task) addNewAssigneeByID(newAssigneeID int64, list *List) (err error) {
	// Check if the user exists and has access to the list
	newAssignee, err := user.GetUserByID(newAssigneeID)
	if err != nil {
		return err
	}
	canRead, _, err := list.CanRead(newAssignee)
	if err != nil {
		return err
	}
	if !canRead {
		return ErrUserDoesNotHaveAccessToList{list.ID, newAssigneeID}
	}

	_, err = x.Insert(TaskAssginee{
		TaskID: t.ID,
		UserID: newAssigneeID,
	})
	if err != nil {
		return err
	}

	err = updateListLastUpdated(&List{ID: t.ListID})
	return
}

// ReadAll gets all assignees for a task
// @Summary Get all assignees for a task
// @Description Returns an array with all assignees for this task.
// @tags assignees
// @Accept json
// @Produce json
// @Param page query int false "The page number. Used for pagination. If not provided, the first page of results is returned."
// @Param per_page query int false "The maximum number of items per page. Note this parameter is limited by the configured maximum of items per page."
// @Param s query string false "Search assignees by their username."
// @Param taskID path int true "Task ID"
// @Security JWTKeyAuth
// @Success 200 {array} user.User "The assignees"
// @Failure 500 {object} models.Message "Internal error"
// @Router /tasks/{taskID}/assignees [get]
func (la *TaskAssginee) ReadAll(a web.Auth, search string, page int, perPage int) (result interface{}, resultCount int, numberOfTotalItems int64, err error) {
	task, err := GetListSimplByTaskID(la.TaskID)
	if err != nil {
		return nil, 0, 0, err
	}

	can, _, err := task.CanRead(a)
	if err != nil {
		return nil, 0, 0, err
	}
	if !can {
		return nil, 0, 0, ErrGenericForbidden{}
	}
	limit, start := getLimitFromPageIndex(page, perPage)

	var taskAssignees []*user.User
	query := x.Table("task_assignees").
		Select("users.*").
		Join("INNER", "users", "task_assignees.user_id = users.id").
		Where("task_id = ? AND users.username LIKE ?", la.TaskID, "%"+search+"%")
	if limit > 0 {
		query = query.Limit(limit, start)
	}
	err = query.Find(&taskAssignees)
	if err != nil {
		return nil, 0, 0, err
	}

	numberOfTotalItems, err = x.Table("task_assignees").
		Select("users.*").
		Join("INNER", "users", "task_assignees.user_id = users.id").
		Where("task_id = ? AND users.username LIKE ?", la.TaskID, "%"+search+"%").
		Count(&user.User{})
	return taskAssignees, len(taskAssignees), numberOfTotalItems, err
}

// BulkAssignees is a helper struct used to update multiple assignees at once.
type BulkAssignees struct {
	// A list with all assignees
	Assignees []*user.User `json:"assignees"`
	TaskID    int64        `json:"-" param:"listtask"`

	web.CRUDable `json:"-"`
	web.Rights   `json:"-"`
}

// Create adds new assignees to a task
// @Summary Add multiple new assignees to a task
// @Description Adds multiple new assignees to a task. The assignee needs to have access to the list, the doer must be able to edit this task. Every user not in the list will be unassigned from the task, pass an empty array to unassign everyone.
// @tags assignees
// @Accept json
// @Produce json
// @Security JWTKeyAuth
// @Param assignee body models.BulkAssignees true "The array of assignees"
// @Param taskID path int true "Task ID"
// @Success 200 {object} models.TaskAssginee "The created assingees object."
// @Failure 400 {object} web.HTTPError "Invalid assignee object provided."
// @Failure 500 {object} models.Message "Internal error"
// @Router /tasks/{taskID}/assignees/bulk [post]
func (ba *BulkAssignees) Create(a web.Auth) (err error) {
	s := x.NewSession()

	task, err := GetTaskByIDSimple(ba.TaskID)
	if err != nil {
		return
	}
	assignees, err := getRawTaskAssigneesForTasks([]int64{task.ID})
	if err != nil {
		return err
	}
	for _, a := range assignees {
		task.Assignees = append(task.Assignees, &a.User)
	}

	err = task.updateTaskAssignees(s, ba.Assignees)
	if err != nil {
		_ = s.Rollback()
		return err
	}

	return s.Commit()
}
