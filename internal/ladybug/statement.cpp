#include "lbug.hpp"

// kg_statement_type returns lbug::common::StatementType of a prepared statement made by the C API. The C API does not
// expose the type, and the read-only mode of the engine lets statements such as COPY TO, EXPORT DATABASE, INSTALL and
// CHECKPOINT write outside the database file.
extern "C" int kg_statement_type(void* prepared_statement) {
    return static_cast<int>(
        static_cast<lbug::main::PreparedStatement*>(prepared_statement)->getStatementType());
}
