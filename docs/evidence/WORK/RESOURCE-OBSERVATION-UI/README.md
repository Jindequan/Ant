# Resource balance observation UI — visual review

The resource page was reviewed at 1280×720 and 390×844 with the observation
form open. The browser reported document widths of 1280 and 390 respectively;
the table remains at its existing ten columns, and the mobile form inputs are
308px wide. Screenshots are `desktop-1280.png` and `mobile-390.png`.

This is layout evidence only. The page was served by Vite and read a clearly
synthetic local visual fixture from a temporary loopback mock API. No provider
was contacted, no balance was recorded, and these screenshots do not prove
real API or account behavior. The real UI/API request path is covered by the
Web test suite; server behavior is covered by the existing SQLite and API
tests.
