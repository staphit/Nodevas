# Timeline and progress

Nodevas keeps planned work separate from actual lifecycle history.

## Planned milestones

Planned milestones describe what should happen and when. The built-in milestones are Started, In progress, and Done; projects may define additional milestone types.

A milestone can have a date and an optional time. Selecting a date saves it immediately. Clearing the date removes the milestone, and a time can be added after a date is selected.

## Actual events

Actual status transitions describe what happened. They are appended to `run/journal.jsonl` with the event time and optional note.

Choosing a new status stages the change in the information panel. Press **Apply status** or `Ctrl+S` to append the event.

## Date assessment

The timeline compares a planned milestone with the matching actual transition and reports:

- on time;
- early;
- late;
- due today;
- overdue;
- pending.

If an actual transition has not happened, Nodevas compares the planned date with today. It calculates the comparison but does not invent a schedule; people or agents still choose planned dates.

## Adjusting history

Dragging an actual timeline card changes its recorded date and creates an adjustment record. The audit list keeps both the original event and the later adjustment.

The [daily work schedule demo](../demo/daily-work-schedule) shows planned milestones and automatic timeline assessment.
