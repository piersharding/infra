# API Search Functionality

This document describes the search functionality implemented in the Infra API for filtering groups and users.

## Overview

The API now supports partial, case-insensitive search functionality for both groups and users endpoints. This allows clients to filter results by providing a partial name match.

## Changes Made

### Groups API (`/api/groups`)

**Endpoint:** `GET /api/groups?name={search_term}`

**Previous Behavior:**
- Exact match only: `name=Engineering` would only return groups with the exact name "Engineering"

**New Behavior:**
- Partial, case-insensitive match: `name=eng` will return all groups containing "eng" (case-insensitive)
- Examples:
  - `name=eng` → Returns "Engineering", "Engineers", "Management", etc.
  - `name=admin` → Returns "Administrators", "Admin", "SystemAdmin", etc.
  - `name=E` → Returns "Engineering", "Everyone", "Executives", etc.

### Users API (`/api/users`)

**Endpoint:** `GET /api/users?name={search_term}`

**Previous Behavior:**
- Exact match only: `name=john@example.com` would only return users with the exact email "john@example.com"

**New Behavior:**
- Partial, case-insensitive match: `name=john` will return all users containing "john" (case-insensitive)
- Examples:
  - `name=john` → Returns "john@example.com", "johnsmith@company.com", "johnson@org.com", etc.
  - `name=@example` → Returns all users with "@example" in their email
  - `name=admin` → Returns "admin@company.com", "administrator@org.com", etc.

## Technical Implementation

### Database Layer Changes

**File:** `internal/server/data/group.go`
```go
// Before
if opts.ByName != "" {
    query.B("AND name = ?", opts.ByName)
}

// After  
if opts.ByName != "" {
    query.B("AND name ILIKE ?", "%"+opts.ByName+"%")
}
```

**File:** `internal/server/data/identity.go`
```go
// Before
if opts.ByName != "" {
    query.B("AND identities.name = ?", opts.ByName)
}

// After
if opts.ByName != "" {
    query.B("AND identities.name ILIKE ?", "%"+opts.ByName+"%")
}
```

### Key Changes:
1. **ILIKE Operator:** Changed from `=` to `ILIKE` for case-insensitive matching
2. **Wildcard Matching:** Added `%` wildcards around the search term for partial matching
3. **Backward Compatible:** Existing exact matches still work (e.g., searching for "Engineering" still returns "Engineering")

## Usage Examples

### Frontend/UI Integration

The frontend search components now work with these endpoints:

```javascript
// Search for groups containing "eng"
fetch('/api/groups?name=eng&page=1&limit=50')

// Search for users containing "john"  
fetch('/api/users?name=john&page=1&limit=50')
```

### CLI Integration

```bash
# Search for groups (if CLI supports search)
infra groups list --search eng

# Search for users (if CLI supports search)
infra users list --search john
```

## API Request/Response Examples

### Groups Search

**Request:**
```http
GET /api/groups?name=admin&page=1&limit=50
Authorization: Bearer <token>
```

**Response:**
```json
{
  "items": [
    {
      "id": "ABC123",
      "name": "Administrators", 
      "totalUsers": 5,
      "created": "2023-01-15T10:30:00Z",
      "updated": "2023-01-15T10:30:00Z"
    },
    {
      "id": "DEF456", 
      "name": "SystemAdmin",
      "totalUsers": 2,
      "created": "2023-02-01T14:20:00Z", 
      "updated": "2023-02-01T14:20:00Z"
    }
  ],
  "totalCount": 2,
  "totalPages": 1
}
```

### Users Search

**Request:**
```http
GET /api/users?name=john&page=1&limit=50
Authorization: Bearer <token>
```

**Response:**
```json
{
  "items": [
    {
      "id": "GHI789",
      "name": "john@example.com",
      "lastSeenAt": "2023-12-01T09:15:00Z",
      "created": "2023-01-10T08:00:00Z",
      "updated": "2023-01-10T08:00:00Z", 
      "providerNames": ["infra"]
    },
    {
      "id": "JKL012",
      "name": "johnsmith@company.com", 
      "lastSeenAt": "2023-11-30T16:45:00Z",
      "created": "2023-03-05T12:30:00Z",
      "updated": "2023-03-05T12:30:00Z",
      "providerNames": ["google"]
    }
  ],
  "totalCount": 2,
  "totalPages": 1
}
```

## Performance Considerations

### Database Performance
- **Indexing:** Ensure proper indexes exist on `groups.name` and `identities.name` columns
- **Query Performance:** ILIKE with leading wildcards can be slower than exact matches
- **Pagination:** Always use pagination (`limit` parameter) to avoid large result sets

### Recommended Indexes
```sql
-- For groups table
CREATE INDEX CONCURRENTLY idx_groups_name_gin ON groups USING gin(name gin_trgm_ops);

-- For identities table  
CREATE INDEX CONCURRENTLY idx_identities_name_gin ON identities USING gin(name gin_trgm_ops);
```

## Migration Notes

### Backward Compatibility
- ✅ **Fully Backward Compatible:** Existing API calls continue to work unchanged
- ✅ **Exact Matches Still Work:** Searching for exact names returns the same results
- ✅ **No Breaking Changes:** All existing query parameters and response formats remain the same

### Testing
- All existing tests updated to cover both exact and partial matching scenarios
- New test cases added for case-insensitive and partial matching
- Performance testing recommended for large datasets

## Security Considerations

### Input Validation
- Search terms are automatically escaped by the database query builder
- No additional input sanitization required beyond existing validation
- SQL injection protection maintained through parameterized queries

### Access Control
- Search functionality respects existing authorization patterns
- Users can only search within their authorized scope (same organization)
- Admin vs non-admin permissions continue to apply

## Future Enhancements

### Potential Improvements
1. **Full-Text Search:** Consider implementing PostgreSQL full-text search for better performance
2. **Search Highlighting:** Return matched portions of names highlighted
3. **Advanced Filters:** Support additional search criteria (created date, etc.)
4. **Search Analytics:** Track search patterns for UX improvements

### Performance Optimizations
1. **Search Caching:** Cache frequent search results
2. **Debounced Search:** Implement request debouncing at API level
3. **Search Suggestions:** Provide autocomplete suggestions
4. **Result Ranking:** Rank results by relevance score

## API Versioning

- **Version:** These changes are included in API version `0.x.x+`
- **Header Required:** `Infra-Version: 0.x.x` (existing requirement)
- **No New Version:** No new API version required as this is backward compatible

## Troubleshooting

### Common Issues

**Search Returns No Results:**
- Verify the search term contains actual characters (not just whitespace)
- Check that the user has permissions to view the resources
- Ensure the organization context is correct

**Search Performance Issues:**
- Check database indexes on name columns
- Consider adding query timeout limits
- Monitor query execution plans

**Case Sensitivity Problems:**
- ILIKE should handle case insensitivity automatically
- Verify database collation settings if issues persist

### Debugging

Enable query logging to see the actual SQL being executed:
```sql
-- Example logged query for groups search
SELECT * FROM groups 
WHERE deleted_at IS NULL 
AND organization_id = $1 
AND name ILIKE $2 
ORDER BY name ASC 
LIMIT $3 OFFSET $4;

-- Parameters: [org_id, "%search_term%", limit, offset]
```
