# object-introspection

## Purpose

Schema discovery tools that let a client enumerate database objects and inspect
their structure: `list_objects` (tables, views, sequences, extensions) and
`get_object_details` (columns, constraints, indexes for a table or view).

## Requirements

### Requirement: list_objects tool

The system SHALL expose a `list_objects` tool that returns objects in a database, accepting an optional `schema` filter and an optional `type` filter. Supported types SHALL include at least `table`, `view`, `sequence`, and `extension`. Each returned object SHALL carry its `schema` (where applicable), `name`, and `type`.

#### Scenario: List tables in a schema

- **WHEN** `list_objects` is called with `database: "primary"`, `schema: "public"`, `type: "table"`
- **THEN** the result contains every base table in the `public` schema, each with `schema`, `name`, and `type: "table"`

#### Scenario: List views

- **WHEN** `list_objects` is called with `type: "view"` and no schema filter
- **THEN** the result contains every view across all non-system schemas

#### Scenario: Unsupported type errors

- **WHEN** `list_objects` is called with `type: "materialized_view"` and the dialect does not support it
- **THEN** the tool returns an error listing the supported types

### Requirement: get_object_details tool

The system SHALL expose a `get_object_details` tool that returns detail for one object given its `schema` and `name`, with an optional `type` (default `table`). For `table` and `view` types the result SHALL include columns (name, data type, nullable, default), constraints (name, type, columns), and indexes (name, definition).

#### Scenario: Table detail includes columns, constraints, indexes

- **WHEN** `get_object_details` is called for an existing table
- **THEN** the result includes a `columns` array ordered by ordinal position, a `constraints` array (primary keys, foreign keys, unique), and an `indexes` array with each index's definition

#### Scenario: Columns reflect nullability and defaults

- **WHEN** a table column is defined `NOT NULL DEFAULT 0`
- **THEN** the corresponding column entry reports `is_nullable: false` and a `default` of `0`

#### Scenario: Unknown object errors

- **WHEN** `get_object_details` is called for a schema/name that does not exist
- **THEN** the tool returns an error indicating the object was not found
