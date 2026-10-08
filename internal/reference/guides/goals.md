# Reading goals

```graphql
query Goals($me: Int!) {
  goals(where: {user_id: {_eq: $me}, archived: {_eq: false}}, order_by: {end_date: desc}) {
    id
    description
    metric
    goal
    progress
    start_date
    end_date
    state
    completed_at
  }
}
```

`progress` is how far along the goal is, in its `metric`.

## Create a goal

The values `metric` takes are not documented; read an existing goal's
`metric` first and reuse it. `conditions` narrows what counts (formats,
categories); `{}` counts everything. Ask who should see the goal (privacy
1 Public, 2 Followers, 3 Private); if the person doesn't say, use 3.

```graphql
mutation NewGoal($goal: Int!, $metric: String!, $description: String!, $start: date!, $end: date!, $privacy: Int!) {
  insert_goal(object: {goal: $goal, metric: $metric, description: $description, start_date: $start, end_date: $end, conditions: {}, privacy_setting_id: $privacy}) {
    id
    errors
    goal { id }
  }
}
```
