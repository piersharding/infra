# Infra Dashboard

This directory contains the source code for the Infra Dashboard.

## Features

- **Context Search**: Real-time search functionality for groups and users with debounced server-side filtering
- **Mobile Responsive**: Optimized interface that works seamlessly on desktop and mobile devices
- **Accessibility**: Full keyboard navigation and screen reader support
- **URL State Management**: Search queries persist in browser URLs for easy bookmarking and sharing

## Set up

- Install the latest version of Node.js `brew install node`
- Install the following extensions if using Visual Studio Code:
  - [ESLint](https://marketplace.visualstudio.com/items?itemName=dbaeumer.vscode-eslint)
  - [Prettier](https://marketplace.visualstudio.com/items?itemName=esbenp.prettier-vscode)
  - [Tailwind CSS IntelliSense](https://marketplace.visualstudio.com/items?itemName=bradlc.vscode-tailwindcss)
  - Enable **Format on Save** in settings

## Develop

Follow the instructions in the [test config file](./__test__/__files__/infra.yaml).

```
npm install
npm run dev
```

## Build and run

```
npm run build
npm start
```

## Linting

Linting is done via [ESLint](https://eslint.org/)

```
npm run lint
```

## Formatting

Code is formatted using [Prettier](https://prettier.io/)

To check for issues:

```
npm run format
```

To fix:

```
npm run format:fix
```

## Search Functionality

The dashboard includes context search functionality that allows users to filter lists by typing search queries. This feature is implemented using reusable components:

- **SearchInput Component** (`/components/search-input.js`) - Provides the search interface
- **useSearch Hook** (`/lib/useSearch.js`) - Manages search state, debouncing, and URL synchronization

For detailed implementation guide, see [Search Functionality Documentation](./docs/search-functionality.md).

### Quick Usage

```javascript
import { useSearch } from '../lib/useSearch'
import SearchInput from '../components/search-input'

const { searchQuery, setSearchQuery, isSearching, buildApiUrl } = useSearch()

<SearchInput
  searchQuery={searchQuery}
  setSearchQuery={setSearchQuery}
  isSearching={isSearching}
  placeholder="Search items..."
/>
```
