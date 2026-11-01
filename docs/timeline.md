# Timeline and progress

Nodevas keeps planned work separate from actual lifecycle history.

## Planned milestones

Planned milestones describe what should happen and when. The built-in milestones are **Start** and **Deadline**; a project can add its own types in **Settings → This project → Milestone types**. A deadline cannot be earlier than its start.

A milestone can have a date and an optional time. Selecting a date saves it immediately. Clearing the date removes the milestone, and a time can be added after a date is selected.

The timeline opens on today or, when nothing planned or recorded is in view there, on the next day that has something. It can run vertically or horizontally and can collapse empty dates.

## Actual events

Actual status transitions describe what happened. They are appended to `run/journal.jsonl` with the event time and optional note.

Choosing a new status in the node panel's top row stages the change. Press **Apply status** or Ctrl/⌘+S to append the event.

## Date assessment

The node panel's **Activity** tab compares the built-in Start and Deadline milestones with the matching actual transitions, in a **Start** line and a **Completion** line, and reports:

- on time;
- early;
- late;
- due today;
- overdue;
- pending;
- no planned date.

If an actual transition has not happened, Nodevas compares the planned date with today. It calculates the comparison but does not invent a schedule; people or agents still choose planned dates. Overdue nodes across the board are listed under **Problems → Schedule**.

## Adjusting history

Dragging an actual timeline card changes its recorded date and creates an adjustment record. The **Actual change order** list in the Activity tab keeps both the original event and the later adjustment.

The [daily work schedule demo](../demo/daily-work-schedule) shows planned milestones and automatic timeline assessment.
